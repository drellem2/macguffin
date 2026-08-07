package workitem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A CLAIM USED AS A HOLD (mg-ed7b).
//
// The gh-issue triage protocol had a polecat append its verdict packet to the
// ticket BODY and leave the item claimed, because `mg done` refuses a
// declares-remainder item that names no successor and the successor cannot be
// filed until after the human gate. The claim was doing the job of a gate.
//
// On 2026-08-07 a sweeper collected seven claims held by polecats that had died
// with their daemon and released the five it judged safe, checking each one
// individually for a pushed branch and a merged commit. Every one of the five
// was a triage: no branch and no commit exist for that work by construction, so
// a finished triage and an abandoned claim were the same thing under that test.
// mg-24d2 landed in available/ at 18:24:18Z with no assignee and did not get one
// until 18:27:15Z; a priority-wake named it "ready and unclaimed" inside that
// window.
//
// mg is not blind the way the sweeper was. All five carried
// `declares-remainder` and four named no successor, and mg held that at the
// moment of each release. These tests pin the two things it now does with it:
// it lets the caller record WHO the item waits on before the item is reachable,
// and it SAYS what it is releasing. Neither is a refusal — a sweep of genuinely
// stranded claims has to stay one command that works.

// claimForTest puts a freshly created item into claimed/ and returns its id.
func claimForTest(t *testing.T, root, id string) {
	t.Helper()
	if _, err := Claim(root, id, 4242); err != nil {
		t.Fatalf("Claim %s: %v", id, err)
	}
}

// --- The assignee lands before the item does --------------------------------

// TestUnclaimRecordsTheAssigneeOnTheReleasedItem: the reason the item is held
// must be ON the item once it is reachable, not in the head of the agent that
// released it.
func TestUnclaimRecordsTheAssigneeOnTheReleasedItem(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage awaiting a ruling", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	res, err := Unclaim(root, item.ID, WithUnclaimAssignee("human"))
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.Assignee != "human" {
		t.Errorf("res.Assignee = %q, want human", res.Assignee)
	}

	landed, err := readFile(filepath.Join(root, "work", "available", item.ID+".md"))
	if err != nil {
		t.Fatalf("reading the released item: %v", err)
	}
	if landed.Assignee != "human" {
		t.Errorf("released item carries assignee %q, want human — it is dispatchable with nothing saying who waits on it", landed.Assignee)
	}

	e := findEvent(t, readEvents(t, root), "work.unclaim")
	if e.Extra["assignee"] != "human" {
		t.Errorf("work.unclaim assignee = %q, want human", e.Extra["assignee"])
	}
	if e.Extra["remainder_owed"] != "" {
		t.Errorf("remainder_owed = %q on an item that declares nothing", e.Extra["remainder_owed"])
	}
}

// TestUnclaimRefusesWhenTheAssigneeCannotBeRecorded is the ordering test, and it
// is the reason --assignee is a flag on unclaim rather than two commands. The
// release must never happen without the gate it was asked for: an item that
// stays claimed is recovered by re-running, whereas one that reaches available/
// ungated is a live ticket nobody knows is unguarded — the 2m57s mg-24d2 spent
// in exactly that state.
func TestUnclaimRefusesWhenTheAssigneeCannotBeRecorded(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only file is still writable, so the failure cannot be induced")
	}
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "assignee write fails", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	m, err := ResolveUnique(root, item.ID)
	if err != nil {
		t.Fatalf("ResolveUnique: %v", err)
	}
	if err := os.Chmod(m.Path, 0o444); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(m.Path, 0o644) })

	if _, err := Unclaim(root, item.ID, WithUnclaimAssignee("human")); err == nil {
		t.Fatal("Unclaim succeeded despite being unable to record the assignee: the item was released ungated")
	}

	after, err := ResolveUnique(root, item.ID)
	if err != nil {
		t.Fatalf("ResolveUnique after the refusal: %v", err)
	}
	if after.Status != "claimed" {
		t.Errorf("status = %q after a refused release, want claimed", after.Status)
	}
	for _, e := range readEvents(t, root) {
		if e.Type == "work.unclaim" {
			t.Error("a work.unclaim event was emitted for a release that did not happen")
		}
	}
}

// TestUnclaimClearsTheAssigneeWhenAskedTo: --assignee="" is a present flag with
// an empty value, which must clear the field rather than be indistinguishable
// from not passing the flag at all.
func TestUnclaimClearsTheAssigneeWhenAskedTo(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "was assigned", nil, WithAssignee("human"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	res, err := Unclaim(root, item.ID, WithUnclaimAssignee(""))
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.Assignee != "" {
		t.Errorf("res.Assignee = %q, want empty", res.Assignee)
	}

	landed, err := readFile(filepath.Join(root, "work", "available", item.ID+".md"))
	if err != nil {
		t.Fatalf("reading the released item: %v", err)
	}
	if landed.Assignee != "" {
		t.Errorf("released item still carries assignee %q", landed.Assignee)
	}
}

// TestUnclaimReportsAnAssigneeItDidNotSet: the event records the state the
// release PRODUCED, not the argument it was passed, so a reader of the log sees
// whether the item landed gated without having to know how it got that way.
func TestUnclaimReportsAnAssigneeItDidNotSet(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "already assigned", nil, WithAssignee("parked"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.Assignee != "parked" {
		t.Errorf("res.Assignee = %q, want parked", res.Assignee)
	}
	if e := findEvent(t, readEvents(t, root), "work.unclaim"); e.Extra["assignee"] != "parked" {
		t.Errorf("work.unclaim assignee = %q, want parked", e.Extra["assignee"])
	}
}

// --- What the release is carrying -------------------------------------------

// TestUnclaimReportsAnOwedRemainder is the discriminator the sweeper lacked. It
// is keyed on the item's own DECLARATION and not on a stage, a type, a tag
// vocabulary or a body grep, which is why it sees a triage whose only artifact
// is prose exactly as well as it sees a build ticket.
func TestUnclaimReportsAnOwedRemainder(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "triage: verdict IMPLEMENT", nil,
		WithTags([]string{"gh-issue", DeclaresRemainderTag}),
		WithBody("workflow: gh-issue\nstage: gated\ngh: drellem2/pogo#121\n\nThe packet is this body.\n"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if !res.RemainderOwed {
		t.Error("RemainderOwed = false on a declares-remainder item with no successor — this is the exact shape the sweep could not see")
	}
	if e := findEvent(t, readEvents(t, root), "work.unclaim"); e.Extra["remainder_owed"] != "true" {
		t.Errorf("work.unclaim remainder_owed = %q, want true", e.Extra["remainder_owed"])
	}
}

// TestUnclaimDoesNotReportADischargedRemainder: something live tracks the
// recommendation, so releasing the claim strands nothing. Reporting here would
// fire on every ordinary sweep of a discharged item and teach the reader to skip
// the line that matters.
func TestUnclaimDoesNotReportADischargedRemainder(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	build, err := Create(root, "mg-", "task", "build what the triage recommended", nil)
	if err != nil {
		t.Fatalf("Create successor: %v", err)
	}
	item, err := Create(root, "mg-", "task", "triage with its build filed", nil,
		WithTags([]string{DeclaresRemainderTag, SuccessorTag(build.ID)}))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.RemainderOwed {
		t.Errorf("RemainderOwed = true although %s tracks the recommendation", build.ID)
	}
}

// TestUnclaimDoesNotReportAnItemThatDeclaresNothing is the population control.
// Every ordinary task must release exactly as it always has — silently. The
// predicates rejected for the `mg done` guard (type == design, non-terminal
// stage) were rejected on OVER-FIRE counts against the live store, and a report
// that fires on the routine case is removed by whoever it inconveniences just as
// surely as a guard is.
func TestUnclaimDoesNotReportAnItemThatDeclaresNothing(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item, err := Create(root, "mg-", "task", "ordinary work, abandoned mid-flight", nil,
		WithBody("workflow: gh-issue\nstage: build\n\nA non-terminal stage that owes nothing.\n"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	res, err := Unclaim(root, item.ID)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if res.RemainderOwed {
		t.Error("RemainderOwed = true on an item that never declared a remainder")
	}
	if e := findEvent(t, readEvents(t, root), "work.unclaim"); e.Extra["remainder_owed"] != "" {
		t.Errorf("remainder_owed = %q, want absent", e.Extra["remainder_owed"])
	}
}

// --- Who released it ---------------------------------------------------------

// TestUnclaimRecordsItsActor. Every other transition records who (mg-3122); the
// release did not. The five releases of 2026-08-07 were later described as
// "attributed" and the log lines carry no actor at all — a belief about a record
// nobody had re-read. A transition that says what happened but not who did it
// cannot answer the first question asked of it.
func TestUnclaimRecordsItsActor(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)
	t.Setenv("MG_ACTOR", "architect")

	item, err := Create(root, "mg-", "task", "swept", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	claimForTest(t, root, item.ID)

	if _, err := Unclaim(root, item.ID); err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if e := findEvent(t, readEvents(t, root), "work.unclaim"); e.Extra["actor"] != "architect" {
		t.Errorf("work.unclaim actor = %q, want architect", e.Extra["actor"])
	}
}

// --- The refusal that produced the pattern -----------------------------------

// TestRemainderRefusalNamesTheHandoff. `mg done` refuses a declared remainder
// that names no successor, and on the gh-issue track it fires at a moment when
// no id can legally satisfy it — the build ticket is not filed until after the
// human gate. Offering only --successor leaves the agent to improvise a hold,
// and what it improvises is holding the claim. Naming the hand-off does not
// discharge anything: the item keeps its declaration and trips this guard again
// at the next `mg done`, which is what separates it from the retraction the
// refusal still refuses to teach.
func TestRemainderRefusalNamesTheHandoff(t *testing.T) {
	item := &Item{ID: "mg-24d2", Tags: []string{DeclaresRemainderTag}}
	err := errRemainderWithoutSuccessor(item)

	if !strings.Contains(err.Hint, "--successor") {
		t.Errorf("hint = %q, want it to still name --successor first", err.Hint)
	}
	if !strings.Contains(err.Hint, "mg unclaim mg-24d2 --assignee=human") {
		t.Errorf("hint = %q, want it to name the hand-off for an item that cannot yet have a successor", err.Hint)
	}
	if strings.Contains(err.Hint, "--rm-tags") {
		t.Errorf("hint = %q teaches the retraction; the hand-off must not become a bypass", err.Hint)
	}
}
