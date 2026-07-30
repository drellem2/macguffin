package workitem

import (
	"os"
	"strings"
	"testing"
)

// mg-ddf4: `creator` recorded the UNIX USER, so it read `daniel` for every one
// of the 2041 items in the live store — a constant field wearing a name that
// promises authorship. It now resolves like the audit log's actor.
//
// The acceptance criterion in the ticket is specific, and it is the one these
// tests are shaped around: **a fix verified by filing one ticket proves
// nothing**, since one ticket is exactly the case where a constant field looks
// correct. Every positive test here files TWO items as TWO DIFFERENT agents and
// asserts they are distinguishable.

// createAs files an item with POGO_AGENT_NAME set to agent and MG_ACTOR
// scrubbed, so the ambient environment cannot answer for the identity under
// test.
func createAs(t *testing.T, root, agent, title string) *Item {
	t.Helper()
	t.Setenv("MG_ACTOR", "")
	os.Unsetenv("MG_ACTOR")
	t.Setenv("POGO_AGENT_NAME", agent)
	item, err := Create(root, "mg-", "task", title, nil)
	if err != nil {
		t.Fatalf("Create as %q: %v", agent, err)
	}
	return item
}

// TestCreatorDistinguishesTwoAgents is the acceptance check: two items filed by
// two different agents must be distinguishable by their creator.
//
// Both agent names are strings that could have come from nowhere else, and each
// assertion names one exactly. A test that only asserted "creator is non-empty"
// — or that only filed one item — passes with the bug fully present, because
// `daniel` is a non-empty string and one sample cannot reveal a constant.
func TestCreatorDistinguishesTwoAgents(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	first := createAs(t, root, "zzz-probe-filer-one", "first agent's item")
	second := createAs(t, root, "zzz-probe-filer-two", "second agent's item")

	if first.Creator != "zzz-probe-filer-one" {
		t.Errorf("first item Creator = %q, want %q", first.Creator, "zzz-probe-filer-one")
	}
	if second.Creator != "zzz-probe-filer-two" {
		t.Errorf("second item Creator = %q, want %q", second.Creator, "zzz-probe-filer-two")
	}
	if first.Creator == second.Creator {
		t.Fatalf("two agents filed two items and both read creator=%q: the field is still constant", first.Creator)
	}

	// The value must survive the round trip to disk, not just live in the
	// returned struct — a reader reaches for the file, never for Create's
	// return value.
	for _, item := range []*Item{first, second} {
		read, err := Read(root, item.ID)
		if err != nil {
			t.Fatalf("Read %s: %v", item.ID, err)
		}
		if read.Creator != item.Creator {
			t.Errorf("%s: Creator on disk = %q, want %q", item.ID, read.Creator, item.Creator)
		}
	}
}

// TestCreatorIsNotTheUnixUser is the negative control. On this box every agent
// runs as the same unix user, so a fix that quietly kept reading the OS user
// would still pass a "two creators differ" test if the two agents happened to
// be compared against nothing. Assert the substitution is gone by name.
func TestCreatorIsNotTheUnixUser(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	unix := currentUser()
	if unix == "" || unix == "unknown" {
		t.Skip("no resolvable unix user to distinguish from")
	}

	item := createAs(t, root, "zzz-probe-not-the-unix-user", "filed by an agent")
	if item.Creator == unix {
		t.Errorf("Creator = %q, the unix user, with POGO_AGENT_NAME set to %q",
			item.Creator, "zzz-probe-not-the-unix-user")
	}
}

// TestCreatorMGActorOverride: MG_ACTOR outranks POGO_AGENT_NAME, matching the
// audit actor's order exactly. A wrapper script or a test that knows its own
// identity gets the last word.
func TestCreatorMGActorOverride(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	t.Setenv("POGO_AGENT_NAME", "zzz-probe-env-agent")
	t.Setenv("MG_ACTOR", "zzz-probe-explicit-actor")

	item, err := Create(root, "mg-", "task", "override", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.Creator != "zzz-probe-explicit-actor" {
		t.Errorf("Creator = %q, want the MG_ACTOR override %q", item.Creator, "zzz-probe-explicit-actor")
	}
}

// TestCreatorFallsBackToUnixUser: a human at a terminal has neither variable
// set. The OS user is vague on a single-user box, deliberately — vague is
// recoverable and a confident wrong answer is not — but it must still be there
// rather than "unknown".
func TestCreatorFallsBackToUnixUser(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	t.Setenv("MG_ACTOR", "")
	t.Setenv("POGO_AGENT_NAME", "")
	os.Unsetenv("MG_ACTOR")
	os.Unsetenv("POGO_AGENT_NAME")

	item, err := Create(root, "mg-", "task", "filed by a human", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := currentUser()
	if want == "" {
		want = "unknown"
	}
	if item.Creator != want {
		t.Errorf("Creator = %q, want the OS user %q", item.Creator, want)
	}
}

// TestCreatorAgreesWithAuditActor pins the two fields to ONE resolution. They
// drifted apart in the first place because each was written separately against
// the same wrong intuition; the guard against a third divergence is that they
// call the same function, and this asserts it end to end rather than by
// inspection.
func TestCreatorAgreesWithAuditActor(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	item := createAs(t, root, "zzz-probe-agreement", "same caller, two fields")
	if got := actor(); item.Creator != got {
		t.Errorf("Creator = %q but audit actor = %q for the same caller", item.Creator, got)
	}
}

// TestCreatorIsSelfAsserted documents the limit in an executable form, so the
// forgeability is a tested property rather than a comment someone can miss.
// POGO_AGENT_NAME is an ordinary environment variable and every agent
// authenticates as the same unix user, so any agent can file as any name. This
// test PASSING is the constraint: creator is attribution, not authentication,
// and nothing may gate access on it.
func TestCreatorIsSelfAsserted(t *testing.T) {
	root := t.TempDir()
	setupDirs(t, root)

	impostor := createAs(t, root, "mayor", "filed by something claiming to be the mayor")
	if impostor.Creator != "mayor" {
		t.Fatalf("Creator = %q, want %q — the test's premise no longer holds", impostor.Creator, "mayor")
	}
	if !strings.HasPrefix(impostor.ID, "mg-") {
		t.Errorf("unexpected ID %q", impostor.ID)
	}
}
