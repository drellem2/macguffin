package main

import (
	"strings"
	"testing"
)

// The shelve guards at the CLI seam (mg-2cf0). The predicates are proven in
// internal/workitem/shelveguard_test.go; what is proven here is what an operator
// standing at the terminal actually gets: a NON-ZERO EXIT, the item still where
// it was, a refusal that names its own remedy and not the bypass, and the
// cascade named rather than merely happening.
//
// The exit code matters on its own. Every one of these paths is reached by an
// agent in a script, and a refusal that exits 0 is a refusal nothing downstream
// can see.

// seedShelvable creates an item and leaves it available, returning its id.
func seedShelvable(t *testing.T, bin, root, typ, title string, extra ...string) string {
	t.Helper()
	args := append([]string{"new", "--type=" + typ, title}, extra...)
	out, code := mgArchive(t, bin, root, args...)
	if code != 0 {
		t.Fatalf("mg new: exit %d\n%s", code, out)
	}
	_, rest, ok := strings.Cut(out, "Created ")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	id, _, ok := strings.Cut(rest, ":")
	if !ok {
		t.Fatalf("could not parse id from %q", out)
	}
	return strings.TrimSpace(id)
}

// statusOf reports the status mg gives for an item.
func statusOf(t *testing.T, bin, root, id string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", id, code, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "Status:"); ok {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("no Status: line in %q", out)
	return ""
}

// --- The refusals, proven first ----------------------------------------------

// TestCLI_ShelveBlockedOnIsRefused is the mg-e925 shape.
func TestCLI_ShelveBlockedOnIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "task", "waiting on a ruling", "--tags=blocked-on-daniel")

	out, code := mgArchive(t, bin, root, "shelve", id)
	if code == 0 {
		t.Fatalf("mg shelve on an item tagged blocked-on-daniel exited 0 — the guard did not fire\n%s", out)
	}
	if !strings.Contains(out, "blocked-on-daniel") {
		t.Errorf("refusal = %q, want it to name the tag it found", out)
	}
	if !strings.Contains(out, "--rm-tags") {
		t.Errorf("refusal = %q, want it to name the remedy that discharges the tag", out)
	}
	if st := statusOf(t, bin, root, id); st == "shelved" {
		t.Error("the item was shelved despite the refusal")
	}
}

// TestCLI_ShelveUntrackedDesignIsRefused is the mg-a08c shape: a design whose
// build nothing carries.
func TestCLI_ShelveUntrackedDesignIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "design", "a design nothing tracks")

	out, code := mgArchive(t, bin, root, "shelve", id)
	if code == 0 {
		t.Fatalf("mg shelve on an untracked design exited 0 — the guard did not fire\n%s", out)
	}
	if !strings.Contains(out, "successor") {
		t.Errorf("refusal = %q, want it to name the successor it wants", out)
	}
	if st := statusOf(t, bin, root, id); st == "shelved" {
		t.Error("the design was shelved despite the refusal")
	}
}

// TestCLI_ShelveTriageIsRefused is the mg-a661 shape: a `type: task` whose body
// carrier block says `stage: triage`. Triage is a position, not a type.
//
// It is filed --no-declares-remainder deliberately, and that is the whole point
// of the arm. Since mg-966d, `mg new` writes the declaration onto a triage body
// by default, so a triage filed TODAY is caught by the tag alone. mg-a661 was
// filed on 2026-07-10, months before the tag existed, and so is every other item
// on the live shelf — 181 of them, of which exactly one carries the tag. The
// carrier block is what reaches the rest, and opting the tag out here is the
// only way to seed that shape.
func TestCLI_ShelveTriageIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "task", "triage: some reported issue",
		"--tags=gh-issue",
		"--no-declares-remainder",
		"--body", "workflow: gh-issue\nstage: triage\ngh: drellem2/pogo#79\n\nTriage this and return a recommendation.")

	out, code := mgArchive(t, bin, root, "shelve", id)
	if code == 0 {
		t.Fatalf("mg shelve on a stage: triage item exited 0 — the carrier-block arm did not fire\n%s", out)
	}
	if !strings.Contains(out, "triage") {
		t.Errorf("refusal = %q, want it to name the block that fired", out)
	}
	if strings.Contains(out, "declares-remainder") {
		t.Errorf("refusal = %q names the tag, but this item has none — the wrong arm is being reported", out)
	}
}

// TestCLI_ShelveRefusalDoesNotTeachTheBypass: an agent that hits a refusal
// mid-cleanup, at speed, reaches for whatever the message hands it. A guard
// whose own failure text names --override is decorative. Same reasoning as
// errNoSuccessor and errBlockedOn.
func TestCLI_ShelveRefusalDoesNotTeachTheBypass(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, seed := range []struct{ typ, title, extra string }{
		{"design", "untracked design", ""},
		{"task", "blocked task", "--tags=blocked-on-daniel"},
	} {
		args := []string{}
		if seed.extra != "" {
			args = append(args, seed.extra)
		}
		id := seedShelvable(t, bin, root, seed.typ, seed.title, args...)
		out, code := mgArchive(t, bin, root, "shelve", id)
		if code == 0 {
			t.Fatalf("expected refusal for %s, got exit 0\n%s", seed.title, out)
		}
		if strings.Contains(out, "--override") {
			t.Errorf("refusal for %s = %q, want it to name the remedy rather than the bypass", seed.title, out)
		}
	}
}

// --- The passing controls ----------------------------------------------------

// TestCLI_ShelveOrdinaryTaskUnchanged is the half that keeps the guard
// installed: a guard that fires on ordinary work gets switched off.
func TestCLI_ShelveOrdinaryTaskUnchanged(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "task", "ordinary work")

	out, code := mgArchive(t, bin, root, "shelve", id)
	if code != 0 {
		t.Fatalf("mg shelve on an ordinary task: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Shelved "+id) {
		t.Errorf("output = %q, want it to report shelving %s", out, id)
	}
	if st := statusOf(t, bin, root, id); st != "shelved" {
		t.Errorf("status = %q, want shelved", st)
	}
}

// TestCLI_ShelveWithSuccessorTagAllowed: the guard is satisfied by a structured
// pointer at an item that still exists.
func TestCLI_ShelveWithSuccessorTagAllowed(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	tracker := seedShelvable(t, bin, root, "task", "carries the build forward")
	id := seedShelvable(t, bin, root, "design", "a tracked design", "--tags=successor:"+tracker)

	out, code := mgArchive(t, bin, root, "shelve", id)
	if code != 0 {
		t.Fatalf("mg shelve on a design naming a live successor: exit %d\n%s", code, out)
	}
	if st := statusOf(t, bin, root, id); st != "shelved" {
		t.Errorf("status = %q, want shelved", st)
	}
}

// --- The override ------------------------------------------------------------

// TestCLI_ShelveOverrideRecordsBothHalves: the override permits the shelve and
// records WHICH guard it bypassed AND WHAT the operator knew that the guard did
// not. A bare --force loses the second, which is the only one a later reader
// cannot reconstruct.
func TestCLI_ShelveOverrideRecordsBothHalves(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedShelvable(t, bin, root, "design", "genuinely abandoned")
	const reason = "abandoned, superseded by the rewrite"

	out, code := mgArchive(t, bin, root, "shelve", id, "--override="+reason)
	if code != 0 {
		t.Fatalf("--override did not permit the shelve: exit %d\n%s", code, out)
	}
	if st := statusOf(t, bin, root, id); st != "shelved" {
		t.Errorf("status = %q, want shelved", st)
	}

	events, _ := mgArchive(t, bin, root, "event", "list", "--type=work.shelve_forced")
	if !strings.Contains(events, id) {
		t.Errorf("no work.shelve_forced event for %s:\n%s", id, events)
	}
	if !strings.Contains(events, reason) {
		t.Errorf("work.shelve_forced does not carry the reason:\n%s", events)
	}
	if !strings.Contains(events, "shelve_without_successor") {
		t.Errorf("work.shelve_forced does not name the guard it bypassed:\n%s", events)
	}
}

// TestCLI_ShelveEmptyOverrideIsNotAnOverride: an override satisfiable by the
// space bar is a boolean wearing a string's clothes. Both the blank and the
// whitespace form must fail, and neither may shelve anything.
func TestCLI_ShelveEmptyOverrideIsNotAnOverride(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, reason := range []string{"", "   "} {
		id := seedShelvable(t, bin, root, "design", "still untracked "+reason+"x")
		out, code := mgArchive(t, bin, root, "shelve", id, "--override="+reason)
		if code == 0 {
			t.Fatalf("--override=%q permitted the shelve\n%s", reason, out)
		}
		if st := statusOf(t, bin, root, id); st == "shelved" {
			t.Errorf("--override=%q shelved the item", reason)
		}
	}
}

// TestCLI_ShelveOverrideRejectedOnTagForm: an override is a claim about ONE item
// the operator looked at; a bulk one is a claim about items they did not.
// Archive refuses --force on its sweep for the same reason.
func TestCLI_ShelveOverrideRejectedOnTagForm(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	seedShelvable(t, bin, root, "design", "untracked but tagged", "--tags=sweep")

	out, code := mgArchive(t, bin, root, "shelve", "--tag=sweep", "--override=because")
	if code == 0 {
		t.Fatalf("--override combined with --tag exited 0\n%s", out)
	}
	if !strings.Contains(out, "--tag") {
		t.Errorf("error = %q, want it to say the two cannot be combined", out)
	}
}

// --- The bulk form -----------------------------------------------------------

// TestCLI_ShelveByTagHonoursTheGuardAndSaysSo: a bulk shelve that skipped the
// guard would be a bypass of the targeted form's refusal one flag away — and a
// bulk shelve that skipped items silently would be indistinguishable from one
// that shelved them.
func TestCLI_ShelveByTagHonoursTheGuardAndSaysSo(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	ok := seedShelvable(t, bin, root, "task", "ordinary tagged", "--tags=sweep")
	guarded := seedShelvable(t, bin, root, "design", "untracked design, tagged", "--tags=sweep")

	out, code := mgArchive(t, bin, root, "shelve", "--tag=sweep")
	if code != 0 {
		t.Fatalf("mg shelve --tag: exit %d\n%s", code, out)
	}
	if st := statusOf(t, bin, root, guarded); st == "shelved" {
		t.Error("the bulk form shelved a guarded item")
	}
	if st := statusOf(t, bin, root, ok); st != "shelved" {
		t.Error("one guarded item stopped the rest of the sweep")
	}
	if !strings.Contains(out, guarded) || !strings.Contains(out, "Skipped") {
		t.Errorf("output = %q, want it to name the item it refused and why", out)
	}
}

// --- The cascade is reported -------------------------------------------------

// TestCLI_ShelveNamesTheCascade is R2 at the seam. Shelving a target hides every
// open item aimed at it; 32 of the 175 items on the live shelf on 2026-07-30 got
// there that way with nothing telling anyone.
func TestCLI_ShelveNamesTheCascade(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	parent := seedShelvable(t, bin, root, "task", "the target")
	child := seedShelvable(t, bin, root, "task", "an audit aimed at the target", "--depends="+parent)

	out, code := mgArchive(t, bin, root, "shelve", parent)
	if code != 0 {
		t.Fatalf("mg shelve: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "Also shelved") {
		t.Errorf("output = %q, want the cascade named as a cascade rather than listed as if the operator had asked for it", out)
	}
	if !strings.Contains(out, child) {
		t.Errorf("output = %q, want it to name %s, which it hid", out, child)
	}
	if !strings.Contains(out, "unshelve") {
		t.Errorf("output = %q, want it to say how to get them back", out)
	}

	events, _ := mgArchive(t, bin, root, "event", "list", "--type=work.shelve")
	if !strings.Contains(events, "dependents") {
		t.Errorf("work.shelve carries no dependents field:\n%s", events)
	}
}
