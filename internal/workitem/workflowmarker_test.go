package workitem

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// Regression tests for mg-5d4e: `--tag=gh-issue` with no `workflow:` body line
// routed silently to the DEFAULT BUILD template, because dispatch keys off the
// body marker and the tag is inert. The near-miss (mg-560d, 2026-07-20) was
// caught by hand at dispatch; a build polecat would otherwise have opened a PR
// against an externally-reported GitHub issue with no human gate.
//
// STORE ISOLATION. Every test here passes an explicit t.TempDir() root to
// Create/Update. That is isolation by construction, not by convention: the
// workitem package never consults $HOME or $MG_ROOT, so these tests cannot reach
// the live store at ~/.macguffin even if the ambient environment says otherwise.
// This matters because mg-da48 records a test suite writing into a LIVE store
// after an isolation guard failed to transfer to a sibling test file — note that
// cmd/mg's tests isolate per-test (tmpHome + HOME= on the exec env) with no
// shared TestMain enforcing it, so that gap is still open there.

// carrierBody is a well-formed gh-issue carrier block, matching the shape the
// mayor's GH-Issue Workflow playbook files.
const carrierBody = `workflow: gh-issue
stage: triage
gh: drellem2/macguffin#25

Triage this GitHub issue: investigate the codebase, consult pm-pogo.`

// wantUsageCode asserts err is a usage-category refusal carrying the given code.
func wantUsageCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected refusal with code %q, got nil error", code)
	}
	var me *mgerr.Error
	if !errors.As(err, &me) {
		t.Fatalf("expected *mgerr.Error, got %T: %v", err, err)
	}
	if me.Code != code {
		t.Errorf("code = %q, want %q (message: %s)", me.Code, code, me.Message)
	}
	if me.Category != mgerr.CatUsage {
		t.Errorf("category = %v, want usage", me.Category)
	}
}

// storeFiles lists the item files under work/, so a refusal can be shown to have
// written nothing at all.
func storeFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(filepath.Join(root, "work"), func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".md") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestCreate_TagWithoutCarrierIsRefused is the POSITIVE CONTROL on the check
// itself: the exact mg-560d shape must fail, and must fail before any write.
// A check that has never been observed to fail is not a check.
func TestCreate_TagWithoutCarrierIsRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "task", "triage: something (o/r#1)", nil,
		WithTags([]string{"gh-issue"}),
		WithBody("Triage this issue. No carrier lines."))

	wantUsageCode(t, err, "workflow_marker_missing")

	// The refusal must leave the store untouched — a half-written item tagged
	// gh-issue sitting in available/ is precisely the dispatchable hazard.
	if files := storeFiles(t, root); len(files) != 0 {
		t.Errorf("refused create wrote %d file(s): %v", len(files), files)
	}
}

// TestCreate_TagWithoutCarrier_MessageNamesTheConsequence pins that the refusal
// explains WHY, not just that it failed. The filer is usually moving fast; a
// bare "invalid" would send them to re-read the playbook instead of fixing it.
func TestCreate_TagWithoutCarrier_MessageNamesTheConsequence(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "task", "triage: x", nil,
		WithTags([]string{"gh-issue"}), WithBody("no carrier"))
	if err == nil {
		t.Fatal("expected refusal")
	}
	var me *mgerr.Error
	if !errors.As(err, &me) {
		t.Fatalf("expected *mgerr.Error, got %T", err)
	}
	// The remediation must hand over the literal carrier block to paste.
	if !strings.Contains(me.Hint, "workflow: gh-issue") || !strings.Contains(me.Hint, "stage:") {
		t.Errorf("hint should contain the carrier skeleton, got:\n%s", me.Hint)
	}
}

// TestCreate_PlaybookCanonicalFilingSucceeds guards against the fix being too
// strict. This is verbatim the shape the GH-Issue Workflow playbook files; if
// this ever refuses, the fix has broken the workflow it exists to protect.
func TestCreate_PlaybookCanonicalFilingSucceeds(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage: real thing (drellem2/macguffin#25)", nil,
		WithTags([]string{"gh-issue"}), WithBody(carrierBody))
	if err != nil {
		t.Fatalf("playbook-canonical filing was refused: %v", err)
	}

	// What dispatch reads is the STORED body, which Render may have rewritten.
	// Assert on the file, not on the in-memory item.
	assertCarrierLeadsStoredBody(t, root, item.ID)
}

// assertCarrierLeadsStoredBody reads the item back off disk and checks the
// carrier block leads the body — no prose above it. This is the property that
// actually determines routing, and it is checked post-Render because Render
// inserts a "# Title" heading above the body when the body lacks one.
func assertCarrierLeadsStoredBody(t *testing.T, root, id string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "work", "available", id+".md"))
	if err != nil {
		t.Fatalf("reading stored item: %v", err)
	}
	_, body, ok := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("could not split frontmatter from body in:\n%s", data)
	}
	name, found, misplaced := leadingWorkflow(body)
	if !found || name != "gh-issue" {
		t.Errorf("stored body does not lead with the gh-issue carrier (found=%v misplaced=%v name=%q); dispatch would misroute it. Body:\n%s",
			found, misplaced, name, body)
	}
}

// TestCreate_BodyCarrierDerivesTheTag is the duality removal: the body is the
// single source of truth and the tag is a projection of it, so a filer who
// writes the carrier block never has to also remember the tag for the board
// query (`mg list --tag=gh-issue`) to stay complete.
func TestCreate_BodyCarrierDerivesTheTag(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage: derived", nil, WithBody(carrierBody))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if !hasTag(item.Tags, "gh-issue") {
		t.Errorf("tags = %v, want the gh-issue tag derived from the body carrier", item.Tags)
	}
}

// TestCreate_DerivedTagIsNotDuplicated: deriving must be idempotent when the
// filer supplied the tag too, which is the common case.
func TestCreate_DerivedTagIsNotDuplicated(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage: both", nil,
		WithTags([]string{"gh-issue", "pogo"}), WithBody(carrierBody))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	var n int
	for _, tag := range item.Tags {
		if tag == "gh-issue" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("gh-issue appears %d times in %v, want exactly 1", n, item.Tags)
	}
	if !hasTag(item.Tags, "pogo") {
		t.Errorf("unrelated tag was dropped: %v", item.Tags)
	}
}

// TestCreate_DivergentWorkflowIsRefused: the tag and the body must not be able
// to assert two different workflows.
func TestCreate_DivergentWorkflowIsRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "task", "t", nil,
		WithTags([]string{"gh-issue"}),
		WithBody("workflow: something-else\nstage: triage\n\nprose"))

	wantUsageCode(t, err, "workflow_marker_conflict")
}

// TestCreate_BuriedCarrierIsRefused: a carrier block below prose reads as
// correctly marked in `mg show` but routes exactly like an unmarked item.
func TestCreate_BuriedCarrierIsRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	_, err := Create(root, "mg-", "task", "t", nil,
		WithTags([]string{"gh-issue"}),
		WithBody("Some preamble prose.\n\nworkflow: gh-issue\nstage: triage"))

	wantUsageCode(t, err, "workflow_marker_misplaced")
}

// TestCreate_UnrelatedTagsAreUntouched: only tags naming a known workflow are
// welded; ordinary labels stay free-form.
func TestCreate_UnrelatedTagsAreUntouched(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "ordinary item", nil,
		WithTags([]string{"pogo", "silent-failure"}), WithBody("Just a normal ticket."))
	if err != nil {
		t.Fatalf("unrelated tags must not be affected, got: %v", err)
	}
	if hasTag(item.Tags, "gh-issue") {
		t.Errorf("tags = %v, gh-issue must not be invented", item.Tags)
	}
}

// TestUpdate_AddingTagToUnmarkedBodyIsRefused closes the same hole on the edit
// path. `mg new` refusing is worthless if `mg edit --add-tags=gh-issue`
// reintroduces the divergence one command later.
func TestUpdate_AddingTagToUnmarkedBodyIsRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "plain build item", nil,
		WithBody("Just build the thing."))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(root, "work", "available", item.ID+".md"))
	if err != nil {
		t.Fatalf("reading item: %v", err)
	}

	_, err = Update(root, item.ID, UpdateField{AddTags: []string{"gh-issue"}})
	wantUsageCode(t, err, "workflow_marker_missing")

	after, err := os.ReadFile(filepath.Join(root, "work", "available", item.ID+".md"))
	if err != nil {
		t.Fatalf("reading item after refusal: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("refused update modified the stored item:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestUpdate_StrippingCarrierFromTaggedItemIsRefused is the other edit-path
// direction: body rewrites are how stages advance, and a careless rewrite that
// drops the carrier would leave a tagged, unroutable item.
func TestUpdate_StrippingCarrierFromTaggedItemIsRefused(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage: real (o/r#7)", nil,
		WithTags([]string{"gh-issue"}), WithBody(carrierBody))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	stripped := "Carrier block removed by a careless rewrite."
	_, err = Update(root, item.ID, UpdateField{Body: &stripped})
	wantUsageCode(t, err, "workflow_marker_missing")
}

// TestUpdate_StageTransitionSucceeds: the normal triage -> build body edit that
// drives the state machine must keep working.
func TestUpdate_StageTransitionSucceeds(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage: real (o/r#8)", nil,
		WithTags([]string{"gh-issue"}), WithBody(carrierBody))
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	advanced := "workflow: gh-issue\nstage: build\ngh: drellem2/macguffin#25\n\nNow building."
	updated, err := Update(root, item.ID, UpdateField{Body: &advanced})
	if err != nil {
		t.Fatalf("stage transition was refused: %v", err)
	}
	if !hasTag(updated.Tags, "gh-issue") {
		t.Errorf("tags = %v, want gh-issue preserved", updated.Tags)
	}
	assertCarrierLeadsStoredBody(t, root, item.ID)
}

// TestLeadingWorkflow is the unit table for the detection rule. The cases that
// matter most are the two "looks right, routes wrong" shapes: a heading above
// the carrier (which Render generates, and which MUST be tolerated) versus prose
// above it (which MUST NOT be).
func TestLeadingWorkflow(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantName      string
		wantFound     bool
		wantMisplaced bool
	}{
		{
			name:      "bare carrier at top",
			body:      "workflow: gh-issue\nstage: triage",
			wantName:  "gh-issue",
			wantFound: true,
		},
		{
			name:      "generated title heading above carrier is tolerated",
			body:      "\n# triage: something\nworkflow: gh-issue\nstage: triage",
			wantName:  "gh-issue",
			wantFound: true,
		},
		{
			name:      "carrier fields may precede workflow",
			body:      "stage: triage\ngh: o/r#1\nworkflow: gh-issue",
			wantName:  "gh-issue",
			wantFound: true,
		},
		{
			name:          "prose above the carrier is misplaced",
			body:          "Please triage this.\n\nworkflow: gh-issue",
			wantName:      "gh-issue",
			wantMisplaced: true,
		},
		{
			name: "no carrier at all",
			body: "# title\n\nJust a normal ticket body.",
		},
		{
			name:     "a prose line containing a colon does not masquerade as a carrier field",
			body:     "# t\nNote: this is prose.\nworkflow: gh-issue",
			wantName: "gh-issue",
			// "Note: ..." is capitalised, so it ends the leading block and the
			// carrier below it is correctly reported as misplaced.
			wantMisplaced: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name, found, misplaced := leadingWorkflow(tc.body)
			if found != tc.wantFound || misplaced != tc.wantMisplaced {
				t.Errorf("found=%v misplaced=%v, want found=%v misplaced=%v",
					found, misplaced, tc.wantFound, tc.wantMisplaced)
			}
			if (found || misplaced) && name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
		})
	}
}

// TestReconcileWorkflowMarkers_DoesNotWrite pins that reconciliation is pure:
// both Create and Update rely on it refusing before they touch the store.
func TestReconcileWorkflowMarkers_DoesNotWrite(t *testing.T) {
	tags, err := reconcileWorkflowMarkers(carrierBody, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasTag(tags, "gh-issue") {
		t.Errorf("tags = %v, want derived gh-issue", tags)
	}
	// Known workflows must be discoverable for help text and diagnostics.
	if got := knownWorkflowTags(); len(got) == 0 || got[0] != "gh-issue" {
		t.Errorf("knownWorkflowTags() = %v, want gh-issue listed", got)
	}
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
