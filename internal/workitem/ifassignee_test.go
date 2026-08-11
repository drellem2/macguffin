package workitem

import (
	"errors"
	"strings"
	"testing"

	"github.com/drellem2/macguffin/internal/mgerr"
)

// mg-5eee's real defect, and the one these DO fail without the fix.
//
// A caller who gates an item has no way to write to it later conditional on the
// gate still being there. `--if-unchanged` covers the body; the dispatch gate,
// which is the field that decides whether the item is worked on at all, had no
// equivalent. So the mayor's four writes to mg-27d4 each printed `Updated` while
// pm-pogo's reassignment stood — nothing lied, nothing was lost, and the hold
// the mayor was relying on was simply gone with no channel to notice through.

func seedGated(t *testing.T, root, assignee string) *Item {
	t.Helper()
	setupDirs(t, root)
	item, err := Create(root, "mg-", "task", "Gated item", nil, WithAssignee(assignee))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return item
}

// TestIfAssignee_RefusesWhenTheGateMoved is the mayor's sequence, compressed:
// gate it, have another agent take it, then try to append.
func TestIfAssignee_RefusesWhenTheGateMoved(t *testing.T) {
	root := t.TempDir()
	item := seedGated(t, root, "blocked:pm-pogo")

	// pm-pogo hands it back — a legitimate edit by another agent.
	other := "mayor"
	if _, err := Update(root, item.ID, UpdateField{Assignee: &other}); err != nil {
		t.Fatalf("reassign: %v", err)
	}

	// The mayor appends, asserting the hold it believes in.
	note := "## a note the mayor writes while believing the item is held\n"
	held := "blocked:pm-pogo"
	_, err := Update(root, item.ID, UpdateField{AppendBody: &note, IfAssignee: &held})
	if err == nil {
		t.Fatal("append succeeded against a moved gate; --if-assignee refused nothing")
	}

	var mgErr *mgerr.Error
	if !errors.As(err, &mgErr) {
		t.Fatalf("error is not an *mgerr.Error: %v", err)
	}
	if mgErr.Code != "assignee_changed" {
		t.Errorf("code = %q, want assignee_changed", mgErr.Code)
	}
	if got := mgErr.ExitCode(); got != 4 {
		t.Errorf("exit code = %d, want 4 (conflict)", got)
	}
	// Both values have to be in the message: "it moved" without naming where it
	// moved TO leaves the caller running another command to find out.
	if !strings.Contains(mgErr.Message, "blocked:pm-pogo") || !strings.Contains(mgErr.Message, `"mayor"`) {
		t.Errorf("message names neither side of the move:\n%s", mgErr.Message)
	}

	// A refusal must leave the item byte-identical — the same guarantee the
	// --if-unchanged and backup refusals make.
	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after.Assignee != "mayor" {
		t.Errorf("refusal changed the assignee to %q", after.Assignee)
	}
	if strings.Contains(after.Body, "a note the mayor writes") {
		t.Error("refusal still appended to the body")
	}
}

// TestIfAssignee_PassesWhenTheGateHeld: the guard must be invisible on the happy
// path, or callers stop passing it.
func TestIfAssignee_PassesWhenTheGateHeld(t *testing.T) {
	root := t.TempDir()
	item := seedGated(t, root, "blocked:pm-pogo")

	note := "## still held\n"
	held := "blocked:pm-pogo"
	if _, err := Update(root, item.ID, UpdateField{AppendBody: &note, IfAssignee: &held}); err != nil {
		t.Fatalf("append with a satisfied precondition was refused: %v", err)
	}

	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(after.Body, "still held") {
		t.Error("append did not land")
	}
	if after.Assignee != "blocked:pm-pogo" {
		t.Errorf("assignee = %q, want blocked:pm-pogo", after.Assignee)
	}
}

// TestIfAssignee_EmptyMeansUnset distinguishes "no precondition" (nil) from "the
// precondition is that nobody holds it" (pointer to ""). Collapsing the two
// would make the guard unusable on exactly the items that most need it: an
// unassigned item is a dispatchable one.
func TestIfAssignee_EmptyMeansUnset(t *testing.T) {
	root := t.TempDir()
	item := seedGated(t, root, "")

	unset := ""
	gate := "blocked:pm-pogo"

	// Requiring unset against an unset field: passes, and takes the gate.
	if _, err := Update(root, item.ID, UpdateField{IfAssignee: &unset, Assignee: &gate}); err != nil {
		t.Fatalf("compare-and-swap onto an unassigned item was refused: %v", err)
	}
	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if after.Assignee != gate {
		t.Fatalf("assignee = %q, want %q", after.Assignee, gate)
	}

	// The same call again must now lose the race rather than steal the gate.
	if _, err := Update(root, item.ID, UpdateField{IfAssignee: &unset, Assignee: &gate}); err == nil {
		t.Fatal("a second compare-and-swap succeeded against an already-gated item")
	}
}

// TestIfAssignee_RunsBeforeTheBodyIsTouched pins the ordering. A precondition
// that ran after the backup or after the write would leave debris behind a
// refusal, which is the property every other guard in this file maintains.
func TestIfAssignee_RunsBeforeTheBodyIsTouched(t *testing.T) {
	root := t.TempDir()
	item := seedGated(t, root, "human")

	body := "# Gated item\n\nwholesale replacement\n"
	want := "parked"
	if _, err := Update(root, item.ID, UpdateField{Body: &body, IfAssignee: &want}); err == nil {
		t.Fatal("replacement succeeded against a failed precondition")
	}

	after, err := Read(root, item.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if strings.Contains(after.Body, "wholesale replacement") {
		t.Error("the body was replaced despite the refusal")
	}
	// And no backup was taken, because nothing was ever at risk.
	backups, err := ListBodyBackups(root, item.ID)
	if err != nil {
		t.Fatalf("ListBodyBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("a refused edit left %d body backup(s) behind", len(backups))
	}
}
