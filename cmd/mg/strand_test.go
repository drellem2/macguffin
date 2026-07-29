package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The operator-facing half of the strand fix. internal/workitem pins the
// placement rule; this pins that an operator is actually TOLD — a silent
// correct placement and a silent wrong one look the same from the terminal,
// and "the only guard we have is people remembering to look" is the class of
// control this change exists to remove.

var strandIDRe = regexp.MustCompile(`mg-[0-9a-f]+`)

// strandStore builds an initialised scratch store and returns a runner bound
// to it via $MG_ROOT.
func strandStore(t *testing.T) (root string, run func(args ...string) string) {
	t.Helper()
	bin := buildBinary(t)
	root = filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	run = func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "MG_ROOT="+root)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("mg %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	run("init")
	return root, run
}

// newItem files an item and returns its minted ID.
func newItem(t *testing.T, run func(...string) string, args ...string) string {
	t.Helper()
	out := run(append([]string{"new"}, args...)...)
	id := strandIDRe.FindString(out)
	if id == "" {
		t.Fatalf("could not find an id in: %s", out)
	}
	return id
}

// TestCLI_NewReportsActualPlacement is the mg-459c strand seen from the
// terminal. `mg new` used to print "(pending)" for any item with a depends
// list — a guess dressed as a fact. An item filed onto an already-completed
// parent is not pending, and saying it is hides the strand at the exact moment
// an operator could have caught it.
func TestCLI_NewReportsActualPlacement(t *testing.T) {
	_, run := strandStore(t)

	parent := newItem(t, run, "task", "the merge")
	run("claim", parent)
	run("done", parent)
	run("archive", parent)

	out := run("new", "audit", "audit of the merge", "--depends", parent)
	if !strings.Contains(out, "(available)") {
		t.Errorf("mg new on an archived parent should report (available), got:\n%s", out)
	}
	if strings.Contains(out, "(pending)") {
		t.Errorf("mg new reported (pending) for a satisfied dependency:\n%s", out)
	}
}

// TestCLI_NewOnShelvedParentSaysSo is the mg-344a / mg-b8f9 strand seen from
// the terminal. Filing behind a shelved gate is allowed — pre-filing gated work
// is a real pattern — but it must name the gate and say how to lift it.
func TestCLI_NewOnShelvedParentSaysSo(t *testing.T) {
	_, run := strandStore(t)

	gate := newItem(t, run, "task", "GATE: pause all one-third work until Mon 2026-07-14")
	run("shelve", gate)

	out := run("new", "task", "one-third deliverable", "--depends", gate)
	if !strings.Contains(out, "(shelved)") {
		t.Errorf("expected the item to land shelved, got:\n%s", out)
	}
	// The notice must name the responsible parent and the way out; a warning
	// that does not name the gate leaves the operator to go looking, which is
	// the failure mode this replaces.
	if !strings.Contains(out, gate) {
		t.Errorf("notice does not name the shelved parent %s:\n%s", gate, out)
	}
	if !strings.Contains(out, "mg unshelve "+gate) {
		t.Errorf("notice does not give the release command:\n%s", out)
	}
}

// TestCLI_NewOnLiveParentStaysQuiet is the other half: a dependent that really
// is waiting must still report pending and must NOT get a strand warning. A
// notice on the healthy path is a notice operators learn to skip.
func TestCLI_NewOnLiveParentStaysQuiet(t *testing.T) {
	_, run := strandStore(t)

	parent := newItem(t, run, "task", "live parent")
	out := run("new", "task", "waits correctly", "--depends", parent)

	if !strings.Contains(out, "(pending)") {
		t.Errorf("a dependent of a live parent should report (pending), got:\n%s", out)
	}
	if strings.Contains(out, "NOTE:") {
		t.Errorf("a correctly-waiting dependent must not warn:\n%s", out)
	}
}

// TestCLI_ScheduleReportsStrands pins the detector's reader. `mg schedule`
// sweeps pending/ and reports what it promoted; it must also report what it
// stepped over and can never promote, naming the parent responsible.
func TestCLI_ScheduleReportsStrands(t *testing.T) {
	root, run := strandStore(t)

	// A dependent that can never be released: its parent does not exist.
	ghost := newItem(t, run, "task", "waits on a ghost", "--depends", "mg-ffff")

	// And one reproducing a store filed before this fix: pending on a shelved
	// parent. Filing now lands it in shelved/, so move it back by hand rather
	// than re-breaking anything live.
	gate := newItem(t, run, "task", "GATE: pause until Monday")
	run("shelve", gate)
	gated := newItem(t, run, "task", "gated deliverable", "--depends", gate)
	if err := os.Rename(
		filepath.Join(root, "work", "shelved", gated+".md"),
		filepath.Join(root, "work", "pending", gated+".md"),
	); err != nil {
		t.Fatal(err)
	}

	out := run("schedule")
	for _, want := range []string{
		"can never be promoted",
		ghost, "mg-ffff", "does not exist",
		gated, gate, "is shelved",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mg schedule output missing %q:\n%s", want, out)
		}
	}
}

// TestCLI_ScheduleQuietWhenHealthy keeps the detector credible: a store whose
// pending items are all waiting on live parents reports no strands.
func TestCLI_ScheduleQuietWhenHealthy(t *testing.T) {
	_, run := strandStore(t)

	parent := newItem(t, run, "task", "live parent")
	newItem(t, run, "task", "waits correctly", "--depends", parent)

	out := run("schedule")
	if strings.Contains(out, "can never be promoted") {
		t.Errorf("healthy store reported strands:\n%s", out)
	}
}
