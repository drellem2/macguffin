package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage of mg edit's title/body coupling (mg-bac6), at the CLI,
// on the exact shape x --title matrix that two agents measured nine days apart
// and drew opposite conclusions from.
//
// The tests are ordered deliberately: the POSITIVE CONTROL comes first, because
// a green run on well-formed input proves nothing. mg-bac6's own post-mortem is
// that the 2026-07-21 probes "passed" on a shape where nothing could go wrong,
// and that is how a coupling defect survived four months and 196 corrupted
// bodies. So the first thing established here is that the new guard CAN fail,
// on the two shapes that used to exit 0.

// seedTitled creates an item whose stored body's first heading is EXACTLY its
// title — the shape every well-formed item has — and returns its ID.
//
// Both fields are named. Passing the title as a positional (as several older
// fixtures did) titles the item "task Whatever" while its body leads with
// "# Whatever", which is itself the corruption shape under test.
func seedTitled(t *testing.T, bin, root, title, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatalf("writing body file: %v", err)
	}
	out, code := mgArchive(t, bin, root, "new", "--type", "task", "--title", title, "--body-file", f, "--no-repo")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	return extractID(t, out)
}

// storedTitleAndHeadings reads the item back and reports the title a reader
// actually gets, plus every "# " heading in the stored body. Reading BACK is the
// point: the whole defect was invisible to the writer, and only a re-read of the
// title plus a heading count revealed it when the mayor hit it on three live
// tickets.
func storedTitleAndHeadings(t *testing.T, bin, root, id string) (string, []string) {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", id, code, out)
	}
	var headings []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "# ") {
			headings = append(headings, strings.TrimPrefix(line, "# "))
		}
	}
	title := ""
	if len(headings) > 0 {
		title = headings[0]
	}
	return title, headings
}

func writeBody(t *testing.T, body string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "new-body.md")
	if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
		t.Fatalf("writing body file: %v", err)
	}
	return f
}

const couplingSubstance = "SENTINEL substance line that must survive byte for byte.\n\n## a section\n\nmore prose\n"

// ---------------------------------------------------------------------------
// POSITIVE CONTROL: the guard can fail.
// ---------------------------------------------------------------------------

// TestCLI_TitleCoupling_PositiveControl_ReplacedHeadingIsRefused is the
// 2026-07-30 shape: --body-file whose first heading is a CORRECTED title, no
// --title passed. This used to exit 0, leave the stale title in place, and
// re-prepend the stored title above the caller's heading — two H1s, and a CLI
// line reporting the body had GROWN on an input file that was smaller.
func TestCLI_TitleCoupling_PositiveControl_ReplacedHeadingIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	const stored = "the stale stored title"
	id := seedTitled(t, bin, root, stored, "# "+stored+"\n\n"+couplingSubstance)
	f := writeBody(t, "# the corrected title\n\n"+couplingSubstance)

	out, code := mgArchive(t, bin, root, "edit", id, "--body-file", f)
	if code == 0 {
		t.Fatalf("edit was ACCEPTED; the guard cannot fail, so its green runs mean nothing\n%s", out)
	}
	if code != 4 {
		t.Errorf("exit %d, want 4 (conflict)\n%s", code, out)
	}
	if !strings.Contains(out, "TITLE") {
		t.Errorf("refusal does not say the title was at stake:\n%s", out)
	}
	// A refusal must leave the item byte-identical, like every other refusal on
	// this write path.
	title, headings := storedTitleAndHeadings(t, bin, root, id)
	if title != stored {
		t.Errorf("refused edit still moved the title to %q", title)
	}
	if len(headings) != 1 {
		t.Errorf("refused edit left %d headings %q, want 1", len(headings), headings)
	}
}

// TestCLI_TitleCoupling_PositiveControl_PrependedHeadingIsRefused is the
// 2026-07-21 shape, including the arm that made that report conclude --title
// "does not protect": a section prepended ABOVE the existing H1, with --title
// naming the title the caller was trying to PRESERVE.
//
// That arm is the nastiest cell in the matrix, because the defensive move was
// what disarmed the defence. The old write guard asked whether "# "+title
// appeared ANYWHERE in the body; passing the current title guaranteed a match
// against the old heading still sitting below the prepended one, so nothing was
// synthesised and the read then took the FIRST heading. The flag that looked
// protective was the one that ensured the clobber, and the success line printed
// the title it had just destroyed.
func TestCLI_TitleCoupling_PositiveControl_PrependedHeadingIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	const stored = "the title being defended"
	id := seedTitled(t, bin, root, stored, "# "+stored+"\n\n"+couplingSubstance)

	// The prepend, with the old heading still present below it.
	prepended := "# a prepended section\n\nnew notes\n\n# " + stored + "\n\n" + couplingSubstance
	f := writeBody(t, prepended)

	// Arm 1: no --title. Refused.
	out, code := mgArchive(t, bin, root, "edit", id, "--body-file", f)
	if code == 0 {
		t.Fatalf("prepend without --title was ACCEPTED; this is the shape that ate mg-2ce4 and mg-0418\n%s", out)
	}
	if code != 4 {
		t.Errorf("exit %d, want 4\n%s", code, out)
	}

	// Arm 2: --title naming the title being preserved. Also refused — the body
	// still renames the item, and saying "keep this title" while handing over a
	// body that starts with a different heading is a contradiction, not a
	// defence. Under the old rule this arm silently retitled the item.
	out, code = mgArchive(t, bin, root, "edit", id, "--title", stored, "--body-file", f)
	if code == 0 {
		title, headings := storedTitleAndHeadings(t, bin, root, id)
		t.Fatalf("prepend WITH the defensive --title was accepted; title is now %q, headings %q\n%s",
			title, headings, out)
	}

	title, headings := storedTitleAndHeadings(t, bin, root, id)
	if title != stored {
		t.Errorf("title moved to %q despite both refusals", title)
	}
	if len(headings) != 1 {
		t.Errorf("stored body has %d headings %q, want 1", len(headings), headings)
	}
}

// ---------------------------------------------------------------------------
// Now, and only now, that ordinary edits pass.
// ---------------------------------------------------------------------------

// TestCLI_TitleCoupling_Matrix runs the full shape x --title matrix and asserts
// the invariant on every accepted cell: the title a reader gets is the title the
// caller named, there is exactly one heading for it, and the substance survived.
//
// The assertion is the invariant, not a literal — no cell pins a heading count
// or title string that a legitimate future change would have to edit.
func TestCLI_TitleCoupling_Matrix(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	const stored = "the stored title"
	const renamed = "the renamed title"

	cases := []struct {
		name string
		// body handed to --body-file
		body string
		// the title flag, or "" for "do not pass --title"
		titleFlag string
		// whether the edit is expected to be refused
		wantRefused bool
		// the title a reader should get afterwards
		wantTitle string
	}{
		// shape (a): prepend a second heading ABOVE the existing H1
		{"a/prepend, no --title", "# prepended\n\n# " + stored + "\n\n" + couplingSubstance, "", true, stored},
		{"a/prepend, --title names the prepended heading",
			"# " + renamed + "\n\nnotes\n\n" + couplingSubstance, renamed, false, renamed},

		// shape (b): replace the existing H1 in place
		{"b/replace heading, no --title", "# " + renamed + "\n\n" + couplingSubstance, "", true, stored},
		{"b/replace heading, --title agrees", "# " + renamed + "\n\n" + couplingSubstance, renamed, false, renamed},

		// shape (c): no heading at all — the procedure that always worked
		{"c/no heading, no --title", couplingSubstance, "", false, stored},
		{"c/no heading, --title renames", couplingSubstance, renamed, false, renamed},

		// shape (d): a blockquoted heading, invisible to the read derivation
		{"d/blockquoted prepend, no --title", "> # quoted\n\n# " + stored + "\n\n" + couplingSubstance, "", false, stored},
		{"d/blockquoted prepend, --title renames", "> # quoted\n\n" + couplingSubstance, renamed, false, renamed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := seedTitled(t, bin, root, stored, "# "+stored+"\n\n"+couplingSubstance)
			f := writeBody(t, tc.body)

			args := []string{"edit", id, "--body-file", f}
			if tc.titleFlag != "" {
				args = []string{"edit", id, "--title", tc.titleFlag, "--body-file", f}
			}
			out, code := mgArchive(t, bin, root, args...)

			if tc.wantRefused {
				if code == 0 {
					t.Fatalf("expected a refusal, got exit 0\n%s", out)
				}
			} else if code != 0 {
				t.Fatalf("expected acceptance, got exit %d\n%s", code, out)
			}

			title, headings := storedTitleAndHeadings(t, bin, root, id)
			if title != tc.wantTitle {
				t.Errorf("stored title %q, want %q", title, tc.wantTitle)
			}
			// The invariant: mg contributes no heading of its own beyond the one
			// carrying the title. So the stored heading count is whatever the
			// body that actually landed had, or 1 if that body had none —
			// derived from the input rather than pinned as a constant, so a
			// legitimate change to any cell's fixture stays self-consistent.
			landed := tc.body
			if tc.wantRefused {
				landed = "# " + stored + "\n\n" + couplingSubstance // the seed survives
			}
			wantHeadings := countH1(landed)
			if wantHeadings == 0 {
				wantHeadings = 1 // mg synthesises exactly one from the title
			}
			if len(headings) != wantHeadings {
				t.Errorf("stored body has %d headings %q, want %d — mg synthesised one of its own",
					len(headings), headings, wantHeadings)
			}
			if !tc.wantRefused && !strings.Contains(readStored(t, bin, root, id), "SENTINEL substance line") {
				t.Error("substance did not survive the edit")
			}
		})
	}
}

// countH1 counts headings by the same rule Parse uses: HasPrefix on the raw
// line, so an indented or blockquoted "> # " is not one.
func countH1(body string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			n++
		}
	}
	return n
}

func readStored(t *testing.T, bin, root, id string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show: exit %d\n%s", code, out)
	}
	return out
}

// TestCLI_TitleCoupling_SuccessLineReportsTheTitleTransition. The success line
// used to print the in-memory title, which on a retitle was the value the write
// had just destroyed — it asserted a title that was already false as it printed.
// A caller who renames an item deliberately should see both ends of the move.
func TestCLI_TitleCoupling_SuccessLineReportsTheTitleTransition(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	const stored = "the old title"
	const renamed = "the new title"
	id := seedTitled(t, bin, root, stored, "# "+stored+"\n\n"+couplingSubstance)

	out, code := mgArchive(t, bin, root, "edit", id, "--title", renamed)
	if code != 0 {
		t.Fatalf("mg edit --title: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, renamed) {
		t.Errorf("success line does not name the new title:\n%s", out)
	}
	if !strings.Contains(out, stored) {
		t.Errorf("success line does not name the title it replaced, so a reader cannot tell what moved:\n%s", out)
	}

	// --title alone stays body-safe: the heading is rewritten in place and the
	// prose is untouched.
	title, headings := storedTitleAndHeadings(t, bin, root, id)
	if title != renamed {
		t.Errorf("stored title %q, want %q", title, renamed)
	}
	if len(headings) != 1 {
		t.Errorf("stored body has %d headings %q, want 1", len(headings), headings)
	}
	if !strings.Contains(readStored(t, bin, root, id), "SENTINEL substance line") {
		t.Error("--title edit did not leave the prose alone")
	}
}

// TestCLI_TitleCoupling_AppendCannotRetitle. An append lands below the prose, so
// it cannot move the first heading — the property that makes --append-body-file
// the safe shape for this coupling as well as for lost updates (mg-f326).
func TestCLI_TitleCoupling_AppendCannotRetitle(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	if out, code := mgArchive(t, bin, root, "init"); code != 0 {
		t.Fatalf("mg init: exit %d\n%s", code, out)
	}

	const stored = "the stored title"
	id := seedTitled(t, bin, root, stored, "# "+stored+"\n\n"+couplingSubstance)

	// Deliberately append a section that LEADS with an H1. It becomes ordinary
	// content, because the title is the FIRST heading and this one is not.
	f := writeBody(t, "# a later section that looks like a title\n\nappended notes\n")
	out, code := mgArchive(t, bin, root, "edit", id, "--append-body-file", f)
	if code != 0 {
		t.Fatalf("mg edit --append-body-file: exit %d\n%s", code, out)
	}

	title, headings := storedTitleAndHeadings(t, bin, root, id)
	if title != stored {
		t.Errorf("an append moved the title to %q", title)
	}
	if len(headings) != 2 {
		t.Errorf("stored body has %d headings %q, want 2 (the title plus the appended one)", len(headings), headings)
	}
	// The extra heading is the caller's own content, so it is reported rather
	// than refused.
	if !strings.Contains(out, "heading") {
		t.Errorf("no note about the extra heading:\n%s", out)
	}
}
