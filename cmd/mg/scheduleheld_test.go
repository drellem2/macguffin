package main

import (
	"strings"
	"testing"
)

// `mg schedule` is the reporting surface for gated work, and it is driven on a
// cron whose whole purpose is that nobody is watching. It used to name a
// snoozed item it could not promote and stay silent about a dependency-gated
// one, so "No items promoted." over a non-empty pending set read as "nothing
// is waiting". These tests pin the report on the whole held population.
//
// The gating itself is not in question here: every case below asserts that the
// item stays in pending/. Only the output changed.

// The case that was invisible: one pending item, held only by a dependency.
func TestCLI_ScheduleReportsADependencyHold(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	driveTheSweep(t, bin, env)

	parent := snzNewItem(t, bin, env, "Parent")
	if out, code := snzRun(t, bin, env, "claim", parent); code != 0 {
		t.Fatalf("mg claim: exit %d\n%s", code, out)
	}
	out, code := snzRun(t, bin, env, "new", "--depends="+parent, "Child")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	child := strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")

	sweep := driveTheSweep(t, bin, env)
	if strings.Contains(sweep, "Promoted ") {
		t.Fatalf("the sweep must not promote a dependency-gated item:\n%s", sweep)
	}
	for _, want := range []string{"held:", child, "depends: ", parent, "(claimed)"} {
		if !strings.Contains(sweep, want) {
			t.Errorf("`mg schedule` output does not contain %q — a held item, its gate and the gate's state must all be named:\n%s", want, sweep)
		}
	}
}

// The positive control. A test asserting only that held items appear would
// pass on an implementation that printed the header unconditionally, so assert
// the report is EMPTY when pending/ is empty.
func TestCLI_ScheduleSaysNothingHeldWhenPendingIsEmpty(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	driveTheSweep(t, bin, env)
	snzNewItem(t, bin, env, "Nothing is gating this")

	sweep := driveTheSweep(t, bin, env)
	if strings.TrimSpace(sweep) != "No items promoted." {
		t.Errorf("over an empty pending/ the sweep must say only that nothing was promoted, got:\n%s", sweep)
	}
	for _, unwanted := range []string{"held", "pending item(s)"} {
		if strings.Contains(sweep, unwanted) {
			t.Errorf("output mentions %q over an empty pending/:\n%s", unwanted, sweep)
		}
	}
}

// The two gates are independent and either can be the one still closed, so an
// item held by both says both. This is also the snooze half's regression
// guard: the wake time and how long is left are still reported.
func TestCLI_ScheduleNamesBothGates(t *testing.T) {
	bin, env, _ := snoozeEnv(t)
	driveTheSweep(t, bin, env)

	parent := snzNewItem(t, bin, env, "Parent")
	out, code := snzRun(t, bin, env, "new", "--depends="+parent, "Child")
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	child := strings.TrimPrefix(strings.Split(out, ":")[0], "Created ")
	if out, code := snzRun(t, bin, env, "snooze", child, "--for", "3d"); code != 0 {
		t.Fatalf("mg snooze: exit %d\n%s", code, out)
	}

	sweep := driveTheSweep(t, bin, env)
	line := ""
	for _, l := range strings.Split(sweep, "\n") {
		if strings.Contains(l, child) {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatalf("the doubly-gated item is not in the report at all:\n%s", sweep)
	}
	for _, want := range []string{"depends: ", parent, "(available)", "snoozed: wakes ", "(in "} {
		if !strings.Contains(line, want) {
			t.Errorf("held line %q does not contain %q — both gates must be named, since either can be the one still closed", line, want)
		}
	}

	// Clearing one gate must leave the other one reported, not the item
	// promoted: this is where a report that conflated the two would show.
	if out, code := snzRun(t, bin, env, "unsnooze", child); code != 0 {
		t.Fatalf("mg unsnooze: exit %d\n%s", code, out)
	}
	sweep = driveTheSweep(t, bin, env)
	if strings.Contains(sweep, "Promoted "+child) {
		t.Fatalf("lifting the snooze promoted an item whose parent is unfinished:\n%s", sweep)
	}
	if !strings.Contains(sweep, "held:") || !strings.Contains(sweep, child) {
		t.Errorf("the surviving dependency gate must still be reported:\n%s", sweep)
	}
	if strings.Contains(sweep, "snoozed:") {
		t.Errorf("a lifted snooze must not still be reported as a gate:\n%s", sweep)
	}
}
