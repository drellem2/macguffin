package workitem

import (
	"errors"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// The negative controls come FIRST in this file on purpose.
//
// mg is the tool every agent on this box files and edits work with. A refusal
// that is too strict does not degrade gracefully — it stops ticket creation
// fleet-wide, immediately, for everyone. So the shapes that must STILL BE
// WRITABLE are the safety case for shipping the refusal at all, and they are
// pinned before the shapes it catches.

// A body may quote carrier syntax, show it in a fenced example, or simply
// contain a colon in a sentence. None of those is a declaration, and refusing
// any of them would mean the ticket documenting the carrier convention could not
// be filed — mg-69b1's body is a live specimen of exactly that.
func TestReachableCarrier_LegitimateBodiesAreNotRefused(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "a fenced example directly under the title",
			body: "# How to write a carrier block\n\n" +
				"Put this at the top of the body:\n\n" +
				"```\nworkflow: gh-issue\nstage: triage\ngh: owner/repo#1\n```\n\n" +
				"and dispatch will route it.\n",
		},
		{
			name: "a fenced example using ~~~ fences",
			body: "# Carrier docs\n\nExample:\n\n~~~\nstage: gated\n~~~\n\nDone.\n",
		},
		{
			name: "prose quoting a carrier line in backticks (mg-69b1's shape)",
			body: "# A gh-issue carrier at stage: gated is still dispatchable\n\n" +
				"Three gh-issue carriers sat at `stage: gated` awaiting a GO/NO-GO.\n" +
				"The playbook says: \"set `stage: gated` and send Daniel the triage packet\".\n",
		},
		{
			name: "an indented quotation of a carrier line",
			body: "# Indented example\n\nThe block looks like:\n\n    stage: gated\n\nwhich is read only at the top.\n",
		},
		{
			name: "a blockquoted carrier line",
			body: "# Quoted\n\nThe filer wrote:\n\n> stage: gated\n\nbut below prose.\n",
		},
		{
			name: "an ordinary sentence with a colon",
			body: "# Ordinary\n\nReported: the daemon wedged overnight and stayed wedged.\n" +
				"Next: work out which watcher should have caught it.\n",
		},
		{
			name: "a key: value line that is not a carrier key",
			body: "# Notes\n\nsome prose first\n\nowner: daniel\nrepo: macguffin\n",
		},
		{
			name: "a correctly placed carrier block",
			body: "# A properly filed item\n\nworkflow: gh-issue\nstage: triage\ngh: owner/repo#1\n\nProse follows.\n",
		},
		{
			name: "a correctly placed carrier block with no blank line under the title",
			body: "# Tight\nworkflow: gh-issue\nstage: gated\n\nProse.\n",
		},
		{
			name: "a body with no carrier syntax at all",
			body: "# Plain\n\nJust some prose about a bug.\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := checkCarrierPlacement(tc.body, ""); err != nil {
				t.Fatalf("refused a legitimate body: %v\nbody:\n%s", err, tc.body)
			}
		})
	}
}

// mg-69b1's own body, as stored. It is the specimen the gate side had to stay
// green on, and the write side has to stay green on it too: the ticket that made
// the stage line load-bearing quotes the transition it is about, repeatedly.
func TestReachableCarrier_MG69B1sOwnBodyStaysWritable(t *testing.T) {
	body := "# A gh-issue carrier at `stage: gated` is still dispatchable — the gate is a convention, not a control\n\n" +
		"Three gh-issue carriers sat at `stage: gated` awaiting a GO/NO-GO, all reading\n" +
		"as gated to a human and as dispatchable to pogod.\n\n" +
		"The playbook (mayor.md, \"State carrier\") says:\n\n" +
		"> When the triage packet arrives, set `stage: gated` and send Daniel the triage packet.\n\n" +
		"and the block it describes is:\n\n" +
		"```\nworkflow: gh-issue\nstage: gated\ngh: drellem2/pogo#104\n```\n\n" +
		"Nothing reads that block at dispatch time. stage: is decoration.\n"
	if err := checkCarrierPlacement(body, ""); err != nil {
		t.Fatalf("mg-69b1's own body must remain writable: %v", err)
	}
}

// PLACEMENT (a): the declaration is below prose, so the block scan stopped
// before reaching it.
func TestUnreachableCarrier_BelowProseIsRefused(t *testing.T) {
	body := "# An item that looks marked\n\n" +
		"This came in from a reporter and needs triage.\n\n" +
		"workflow: gh-issue\nstage: gated\ngh: owner/repo#7\n"
	err := checkCarrierPlacement(body, "")
	if err == nil {
		t.Fatal("a carrier block below prose must be refused")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")
	// The message must name the PLACEMENT, not the symptom: "unreachable" tells
	// a filer nothing, "below prose, move it under the title" is a ten-second fix.
	assertMentions(t, err, "BELOW a line of prose", "workflow: gh-issue", "title heading")
	if !strings.Contains(errText(t, err), "This came in from a reporter") {
		t.Errorf("the refusal should quote the line that ended the scan; got:\n%s", errText(t, err))
	}
}

// PLACEMENT (b): the declaration is above the title heading, where the title
// search consumes it and the block scan never looks. mg-779b and mg-9863 are in
// available/ in exactly this state, held only by `assignee: parked`.
func TestUnreachableCarrier_AboveTitleHeadingIsRefused(t *testing.T) {
	body := "workflow: gh-issue\nstage: gated\ngh: owner/repo#9\n\n" +
		"# An item whose block is above its title\n\nSome prose.\n"
	err := checkCarrierPlacement(body, "")
	if err == nil {
		t.Fatal("a carrier block above the title heading must be refused")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")
	assertMentions(t, err, "ABOVE the body's '# ' title heading", "workflow: gh-issue")
}

// The horizon is mirrored, not tightened: a declaration inside the scan window
// is reachable and must not be refused, and the first one outside it must be.
// These two cases pin the boundary from both sides, which is what makes a future
// drift of carrierReachHorizon visible instead of silent.
func TestUnreachableCarrier_HorizonBoundary(t *testing.T) {
	build := func(offset int) string {
		b := "# Padded\n"
		for i := 0; i < offset; i++ {
			b += "pad-" + string(rune('a'+i%26)) + ": x\n"
		}
		return b + "stage: gated\n"
	}
	// Last line the scan still reads.
	if err := checkCarrierPlacement(build(carrierReachHorizon-1), ""); err != nil {
		t.Fatalf("a declaration at the last readable offset must not be refused: %v", err)
	}
	// One past it.
	err := checkCarrierPlacement(build(carrierReachHorizon), "")
	if err == nil {
		t.Fatalf("a declaration past the %d-line horizon must be refused", carrierReachHorizon)
	}
	assertMentions(t, err, "lines below the '# ' title heading")
}

// A pre-existing unreachable declaration is not this write's fault. Refusing it
// would leave the item with no append-only route to a correction at all — the
// exact trap mg-d878 documents for the missing-carrier case.
func TestUnreachableCarrier_InheritedDeclarationIsNotRefused(t *testing.T) {
	old := "# Legacy\n\nprose first\n\nstage: gated\n"
	appended := old + "\nA correction appended by a later agent.\n"
	if err := checkCarrierPlacement(appended, old); err != nil {
		t.Fatalf("an append onto an already-misplaced body must not be refused: %v", err)
	}
	// But a NEW misplaced declaration in the same write still is.
	worse := appended + "\nworkflow: gh-issue\n"
	if err := checkCarrierPlacement(worse, old); err == nil {
		t.Fatal("a newly introduced misplaced declaration must still be refused")
	}
}

// The refusal has to hold on the real write paths, not only on the predicate:
// mg new and mg edit are where a filer meets it.
func TestUnreachableCarrier_CreateRefuses(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	// A stage-only block below prose. This is the hole the older
	// one-fact-one-marker guard cannot see: it keys off a `workflow:` line, and
	// there isn't one — while `stage: gated` is on its own a DISPATCH GATE.
	_, err := Create(root, "mg-", "task", "An item that looks gated", nil,
		WithBody("This came in from a reporter.\n\nstage: gated\n"))
	if err == nil {
		t.Fatal("Create must refuse a body whose carrier block is below prose")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")

	// And the refusal leaves nothing behind — the same property the
	// one-fact-one-marker guard has.
	if files := storeFiles(t, root); len(files) != 0 {
		t.Fatalf("a refused filing wrote %d file(s): %v", len(files), files)
	}

	// A full carrier block written ABOVE the title heading. This one is invisible
	// to the older guard in the other direction: leadingWorkflow skips heading
	// lines, so it reads the block as correctly placed and would have ADDED the
	// gh-issue tag to an item dispatch cannot read. mg-779b and mg-9863 are in
	// available/ in exactly this shape.
	_, err = Create(root, "mg-", "task", "An item whose block is above its title", nil,
		WithBody("workflow: gh-issue\nstage: gated\ngh: owner/repo#9\n\n# Reporter's heading\n\nProse.\n"))
	if err == nil {
		t.Fatal("Create must refuse a carrier block above the title heading")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")
	assertMentions(t, err, "ABOVE the body's '# ' title heading")

	// A `workflow:`-bearing block below prose is refused too, by the older guard
	// and with its own code (workflow_marker_misplaced) — see
	// workflowmarker_test.go. The two checks are complementary, not layered: that
	// one covers the workflow line's routing, this one covers placement generally.
	_, err = Create(root, "mg-", "task", "Workflow below prose", nil,
		WithBody("Prose first.\n\nworkflow: gh-issue\nstage: gated\n"))
	if err == nil {
		t.Fatal("a workflow line below prose must be refused by one guard or the other")
	}

	// The legitimate shape still files.
	if _, err := Create(root, "mg-", "task", "A documented convention", nil,
		WithBody("Example:\n\n```\nstage: gated\n```\n\nThat is the block.\n")); err != nil {
		t.Fatalf("a fenced example must still be filable: %v", err)
	}
}

func TestUnreachableCarrier_EditRefuses(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "Ordinary item", nil, WithBody("Some prose.\n"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	body := "Some prose.\n\nstage: gated\n"
	_, err = Update(root, item.ID, UpdateField{Body: &body})
	if err == nil {
		t.Fatal("Update must refuse a body whose carrier declaration is below prose")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")

	above := "stage: gated\n\n# Ordinary item\n\nSome prose.\n"
	_, err = Update(root, item.ID, UpdateField{Body: &above})
	if err == nil {
		t.Fatal("Update must refuse a carrier declaration above the title heading")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")

	ok := "workflow: gh-issue\nstage: triage\ngh: owner/repo#3\n\nSome prose.\n"
	if _, err := Update(root, item.ID, UpdateField{Body: &ok}); err != nil {
		t.Fatalf("a correctly placed block must still be writable: %v", err)
	}
}

// errText flattens a refusal to the text a filer actually sees.
func errText(t *testing.T, err error) string {
	t.Helper()
	var me *mgerr.Error
	if !errors.As(err, &me) {
		t.Fatalf("expected *mgerr.Error, got %T: %v", err, err)
	}
	return me.Message + "\n" + me.Hint
}

// assertMentions pins that the refusal names the PLACEMENT. A filer told
// "carrier block unreachable" learns nothing; one told "your stage: line is
// below prose, move it under the title" fixes it in ten seconds.
func assertMentions(t *testing.T, err error, wants ...string) {
	t.Helper()
	text := errText(t, err)
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("refusal does not mention %q; got:\n%s", w, text)
		}
	}
}

// The mayor's shape (mg-f928 mid-flight correction, measured on main's binary
// 2026-08-12): a body whose carrier block CORRECTLY leads it and which carries
// its own '# ' heading further down. mg makes that heading the item's title —
// leaving the block above it, unreadable — and on main this filed exit 0. It is
// mg-779b's and mg-9863's exact layout, produced by mg itself from a body the
// filer got right, which is why the refusal has to name that mechanism rather
// than blame the placement on the filer.
func TestUnreachableCarrier_MgsOwnTitlePlacementIsRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "task", "PARKED awaiting GO/NO-GO", nil,
		WithBody("workflow: gh-issue\nstage: gated\ngh: drellem2/pogo#100\n\n"+
			"# PARKED awaiting Daniel's GO/NO-GO\n\nSuccessor to the triage.\n"))
	if err == nil {
		t.Fatal("a leading carrier block with the filer's own heading below it must be refused")
	}
	wantUsageCode(t, err, "carrier_declaration_unreachable")
	assertMentions(t, err, "YOUR BODY MAY LOOK CORRECT", "carries its OWN '# ' heading")

	// And the shape it points them at works.
	if _, err := Create(root, "mg-", "task", "PARKED awaiting GO/NO-GO", nil,
		WithBody("workflow: gh-issue\nstage: gated\ngh: drellem2/pogo#100\n\nSuccessor to the triage.\n")); err != nil {
		t.Fatalf("the remedy the refusal names must itself file: %v", err)
	}
}

// Negative controls on the TITLE side specifically. This change does not move a
// heading — it refuses — but the guard now reads where headings are, so a body
// with several of them, or one whose first line is deliberately a heading, must
// still file.
func TestReachableCarrier_TitleShapesStillFile(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	cases := []struct{ name, title, body string }{
		{"a body with several headings and no carrier block", "multi",
			"# One\n\nprose\n\n# Two\n\nmore prose\n\n# Three\n"},
		{"a body with no carrier block at all", "plain", "Just prose, no colons of note.\n"},
		{"a body whose intended first line is a heading", "headed", "# headed\n\nprose\n"},
		{"a body with no heading at all (mg synthesises one)", "unheaded", "prose only\n"},
		{"several headings AND a correctly leading carrier block", "carried",
			"workflow: gh-issue\nstage: triage\ngh: o/r#1\n\nprose\n\n## sub\n\nmore\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Create(root, "mg-", "task", tc.title, nil, WithBody(tc.body)); err != nil {
				t.Fatalf("refused an ordinary body: %v", err)
			}
		})
	}
}

// A fenced example containing a `workflow:` line used to be refused outright, by
// the OLDER guard: the fence ended the leading block, the scan walked on into
// the fence, and called the example a misplaced declaration. Measured exit 2 on
// main, 2026-08-12. A ticket documenting the carrier convention could not be
// filed with mg — which is the shape of false refusal this whole change is meant
// not to add, arriving from the other direction.
func TestReachableCarrier_FencedWorkflowExampleFiles(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	if _, err := Create(root, "mg-", "task", "How to write a carrier block", nil,
		WithBody("Put this at the top of the body:\n\n```\nworkflow: gh-issue\nstage: triage\ngh: o/r#1\n```\n\nThen file it.\n")); err != nil {
		t.Fatalf("a fenced carrier example must be filable: %v", err)
	}
}
