package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Regression tests for mg-d878: the workflow-marker guard, which is a
// FILING-TIME check, was being applied to CORRECTION-TIME writes on items filed
// before the carrier block existed. On 2026-07-29, 41 of the 92 items tagged
// gh-issue carried no leading carrier block, and for every one of them the guard
// refused each edit that touched the body — including `--append-body-file`,
// which is the append-only correction protocol mg-f326 exists to make the
// default.
//
// The trap was total: rewrite the body (the clobber mg-f326 forbids),
// --rm-tags=gh-issue (destroys the board query, and re-adding trips the same
// guard), or give up and mail the finding — which is a finding that dies with
// the thread. The mg-d489 audit took the third on mg-ace6.
//
// What follows pins BOTH halves: the append path is open on an inherited
// disagreement, and every direction that would let a write CREATE one stays
// shut. The second half is the load-bearing one — the `!found` refusal is what
// stops a gh-issue item routing to the default build template and opening a PR
// against an externally-reported issue with no human gate (mg-560d).
//
// STORE ISOLATION: as in workflowmarker_test.go, every root here is a
// t.TempDir() passed explicitly; the workitem package never reads $HOME or
// $MG_ROOT, so these cannot reach the live store.

// legacyBody is a gh-issue item as filed before the carrier block was a
// convention: real content, a GitHub issue reference in the prose, and no
// leading `workflow:` line anywhere.
const legacyBody = `# audit: stale assertion in the docs

The tracker says drellem2/pogo#75 is open. Someone should check.`

// writeLegacyItem plants a work item on disk WITHOUT going through Create, which
// is the only way to obtain one of the 41: Create has refused this shape since
// mg-5d4e, so the population the guard traps cannot be produced by mg itself.
// Bypassing the constructor is the point of the fixture, not a shortcut.
func writeLegacyItem(t *testing.T, root, id, body string, tags []string) string {
	t.Helper()
	path := plantItem(t, root, id, body, tags)
	// The fixture is only meaningful if it really is un-appendable under the old
	// rule. Assert the precondition rather than trusting the constant.
	if _, found, _ := leadingWorkflow(readStoredBody(t, path)); found {
		t.Fatalf("fixture body already declares a workflow; it is not a legacy item:\n%s", body)
	}
	return path
}

// plantItem is writeLegacyItem without the no-carrier precondition, for the one
// fixture that deliberately declares a DIFFERENT workflow.
func plantItem(t *testing.T, root, id, body string, tags []string) string {
	t.Helper()
	item := &Item{
		ID:      id,
		Type:    "task",
		Created: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Creator: "legacy-filer",
		Tags:    tags,
		Title:   "audit: stale assertion in the docs",
		Body:    body,
	}
	path := filepath.Join(root, "work", "available", id+".md")
	if err := os.WriteFile(path, []byte(Render(item)), 0o644); err != nil {
		t.Fatalf("planting legacy item: %v", err)
	}
	return path
}

// readStoredBody returns the body as it sits on disk, which is what dispatch
// reads and therefore the only version worth asserting on.
func readStoredBody(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading stored item: %v", err)
	}
	_, body, ok := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("could not split frontmatter from body in:\n%s", data)
	}
	return body
}

// TestUpdate_AppendToLegacyTaggedItemSucceeds is the POSITIVE CONTROL on the
// fix: verbatim the command mg-d489 was told to run and could not.
//
// This is the whole ticket. If it ever starts refusing again, 41 items are
// unreachable by the correction protocol and findings go back to dying in
// inboxes.
func TestUpdate_AppendToLegacyTaggedItemSucceeds(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	path := writeLegacyItem(t, root, "mg-ace6", legacyBody, []string{"gh-issue"})

	correction := "## ANSWERED ELSEWHERE — pogo#75 is FIXED IN CODE\n\nShipped 2026-07-11 in 3f79fac (mg-fdd5)."
	updated, err := Update(root, "mg-ace6", UpdateField{AppendBody: &correction})
	if err != nil {
		t.Fatalf("append to a legacy gh-issue item was refused: %v", err)
	}

	stored := readStoredBody(t, path)
	if !strings.Contains(stored, "ANSWERED ELSEWHERE") {
		t.Errorf("appended correction is not in the stored body:\n%s", stored)
	}
	// An append must compose against what is there, never replace it.
	if !strings.Contains(stored, "drellem2/pogo#75 is open") {
		t.Errorf("append destroyed the original body:\n%s", stored)
	}
	// And the classification that makes `mg list --tag=gh-issue` complete must
	// survive: --rm-tags was one of the wrong ways out, not a side effect.
	if !hasTag(updated.Tags, "gh-issue") {
		t.Errorf("tags = %v, want gh-issue preserved", updated.Tags)
	}
}

// TestUpdate_AppendToLegacyItemLeavesItStillUnmarked pins the honest half of
// grandfathering: mg allows the write, and mg does NOT pretend it repaired
// anything. The item is still one dispatch routes to the default build
// template, and MissingWorkflowCarrier is how a caller finds that out.
//
// mg cannot repair it: the carrier block's other fields name a state-machine
// position and a real GitHub issue, and inventing a `gh:` ref would point a
// build polecat at the wrong issue — strictly worse than saying nothing.
func TestUpdate_AppendToLegacyItemLeavesItStillUnmarked(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	writeLegacyItem(t, root, "mg-ace6", legacyBody, []string{"gh-issue"})

	note := "## a dated section"
	updated, err := Update(root, "mg-ace6", UpdateField{AppendBody: &note})
	if err != nil {
		t.Fatalf("append was refused: %v", err)
	}
	if got := MissingWorkflowCarrier(updated); got != "gh-issue" {
		t.Errorf("MissingWorkflowCarrier = %q, want %q — the append must not read as a repair", got, "gh-issue")
	}
}

// TestUpdate_AppendCannotSmuggleACarrierBlock: the misplaced direction is
// UNCHANGED by the grandfathering, and it is the reason grandfathering is safe
// at all. Appended text lands below the prose by construction, so a carrier
// block written there reads as marked to a human skimming `mg show` while
// routing exactly like an unmarked item. That is the one thing worse than an
// honestly unmarked item.
func TestUpdate_AppendCannotSmuggleACarrierBlock(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	path := writeLegacyItem(t, root, "mg-ace6", legacyBody, []string{"gh-issue"})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading item: %v", err)
	}

	smuggled := "workflow: gh-issue\nstage: triage\ngh: drellem2/macguffin#75"
	_, err = Update(root, "mg-ace6", UpdateField{AppendBody: &smuggled})
	wantUsageCode(t, err, "workflow_marker_misplaced")

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading item after refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("refused append modified the stored item:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestUpdate_AppendKeepsTheConflictRefusal: an append inherits a DIVERGENCE the
// same way it inherits a missing carrier — but a divergence is a body that
// actively declares a different workflow, so the tag is not merely incomplete,
// it is wrong. The routing hazard is live either way, and unlike the missing
// carrier there is a repair mg can point at that does not require inventing a
// `gh:` ref: make the two agree.
func TestUpdate_AppendKeepsTheConflictRefusal(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	plantItem(t, root, "mg-ace6",
		"workflow: something-else\nstage: triage\n\nprose", []string{"gh-issue"})

	note := "## a dated section"
	_, err := Update(root, "mg-ace6", UpdateField{AppendBody: &note})
	wantUsageCode(t, err, "workflow_marker_conflict")
}

// TestUpdate_AppendPlusAddingTheWorkflowTagIsStillRefused is the boundary that
// keeps this a grandfather clause rather than a hole. An append cannot author
// the leading block, so it can only INHERIT a missing carrier — but bolting
// --add-tags=gh-issue onto the same command CREATES the disagreement, which is
// exactly the mg-560d shape. The exemption is keyed on the tag having already
// been there, not on the flag being an append.
func TestUpdate_AppendPlusAddingTheWorkflowTagIsStillRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "plain build item", nil,
		WithBody("Just build the thing."))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	note := "## a dated section"
	_, err = Update(root, item.ID, UpdateField{
		AppendBody: &note,
		AddTags:    []string{"gh-issue"},
	})
	wantUsageCode(t, err, "workflow_marker_missing")
}

// TestUpdate_AppendPlusReplacingTagsWithTheWorkflowTagIsStillRefused is the same
// boundary reached through --tags rather than --add-tags. Whole-set replacement
// and incremental addition are different code paths on the same field, and a
// guard that watched only one of them would be a guard with a documented bypass.
func TestUpdate_AppendPlusReplacingTagsWithTheWorkflowTagIsStillRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "plain build item", nil,
		WithBody("Just build the thing."))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	note := "## a dated section"
	_, err = Update(root, item.ID, UpdateField{
		AppendBody: &note,
		Tags:       []string{"gh-issue"},
	})
	wantUsageCode(t, err, "workflow_marker_missing")
}

// TestUpdate_BodyRewriteOnLegacyItemIsStillRefused: the ticket's explicit "do
// not". A full-body replacement CAN author the leading block, so it has no claim
// to inherit anything — and it is the capture-then-rewrite shape mg-f326 exists
// to prevent. The remedy the guard hands back (paste the carrier block at the
// top) is a real remedy on this path, which is precisely why it stays refused
// here and is lifted on the append.
func TestUpdate_BodyRewriteOnLegacyItemIsStillRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	writeLegacyItem(t, root, "mg-ace6", legacyBody, []string{"gh-issue"})

	rewritten := legacyBody + "\n\n## ANSWERED ELSEWHERE — pogo#75 is FIXED IN CODE"
	_, err := Update(root, "mg-ace6", UpdateField{Body: &rewritten})
	wantUsageCode(t, err, "workflow_marker_missing")
}

// TestUpdate_NonBodyEditsOnLegacyItemsAreStillRefused records a KNOWN, DELIBERATE
// limit of this fix rather than claiming it is not there.
//
// The guard still refuses `mg edit mg-ace6 --priority=high` on a legacy item,
// because the reconciliation runs on the resulting item and that item is still
// unmarked. Only the append path is grandfathered. That is the narrow fix the
// ticket asked for: the append restores the correction protocol, which is the
// thing whose absence loses findings. Widening it to every non-body edit is a
// separate call about a separate population, and quietly folding it in here
// would relax the guard in a direction nobody reviewed.
func TestUpdate_NonBodyEditsOnLegacyItemsAreStillRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	writeLegacyItem(t, root, "mg-ace6", legacyBody, []string{"gh-issue"})

	high := "high"
	_, err := Update(root, "mg-ace6", UpdateField{Priority: &high})
	wantUsageCode(t, err, "workflow_marker_missing")
}

// TestUpdate_AppendToWellFormedItemIsUnaffected: the exemption must not change
// anything for the 51 items that DO carry a carrier block. The append composes
// below the block, the block still leads, the tag still derives.
func TestUpdate_AppendToWellFormedItemIsUnaffected(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "triage: real (o/r#9)", nil,
		WithTags([]string{"gh-issue"}), WithBody(carrierBody))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	note := "## 2026-07-29 — a dated section"
	updated, err := Update(root, item.ID, UpdateField{AppendBody: &note})
	if err != nil {
		t.Fatalf("append to a well-formed item was refused: %v", err)
	}
	assertCarrierLeadsStoredBody(t, root, item.ID)
	if got := MissingWorkflowCarrier(updated); got != "" {
		t.Errorf("MissingWorkflowCarrier = %q, want \"\" on a well-formed item", got)
	}
}

// TestCreate_AppendGrandfatheringDoesNotReachFilings: Create passes the zero
// writeShape, so no filing can ever be grandfathered. A relaxation that leaked
// into the constructor would reopen mg-560d itself.
func TestCreate_AppendGrandfatheringDoesNotReachFilings(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "task", "triage: x", nil,
		WithTags([]string{"gh-issue"}), WithBody("no carrier"))
	wantUsageCode(t, err, "workflow_marker_missing")
}

// TestWriteShapeGrandfathers is the unit table for the exemption's two
// conditions. Both are required; either alone is a hole.
func TestWriteShapeGrandfathers(t *testing.T) {
	cases := []struct {
		name  string
		shape writeShape
		want  bool
	}{
		{
			name:  "append onto an inherited tag",
			shape: writeShape{appendOnly: true, priorTags: []string{"gh-issue", "pogo"}},
			want:  true,
		},
		{
			name:  "append that newly adds the tag",
			shape: writeShape{appendOnly: true, priorTags: []string{"pogo"}},
		},
		{
			name:  "body replacement, tag inherited",
			shape: writeShape{priorTags: []string{"gh-issue"}},
		},
		{
			name:  "a filing carries neither",
			shape: writeShape{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.shape.grandfathers("gh-issue"); got != tc.want {
				t.Errorf("grandfathers(gh-issue) = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMissingWorkflowCarrier is the reporting half, checked directly: it must
// name the tag on an unmarked item, stay silent on a marked one, and never
// invent a complaint about an item with no workflow tag at all.
func TestMissingWorkflowCarrier(t *testing.T) {
	cases := []struct {
		name string
		item *Item
		want string
	}{
		{
			name: "tagged, no carrier",
			item: &Item{Title: "t", Tags: []string{"gh-issue"}, Body: legacyBody},
			want: "gh-issue",
		},
		{
			name: "tagged, carrier leads",
			item: &Item{Title: "t", Tags: []string{"gh-issue"}, Body: carrierBody},
		},
		{
			name: "no workflow tag at all",
			item: &Item{Title: "t", Tags: []string{"pogo"}, Body: legacyBody},
		},
		{
			name: "no tags at all",
			item: &Item{Title: "t", Body: legacyBody},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MissingWorkflowCarrier(tc.item); got != tc.want {
				t.Errorf("MissingWorkflowCarrier = %q, want %q", got, tc.want)
			}
		})
	}
}
