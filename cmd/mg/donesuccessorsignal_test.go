package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two halves of mg-9259, which is one defect wearing two faces.
//
// `mg done --successor=<id>` validated that the id EXISTS and nothing else. An
// id naming nothing was caught (exit 3); a real id naming the WRONG item was
// accepted with exit 0 and no output of any kind, silently gating a live
// pending item on a ticket that could never carry the work.
//
// That would be an ordinary weak check if nothing PUSHED toward it. Something
// did. The declared-remainder guard refused an item naming no successor, and it
// refused BEFORE the result sidecar was written — so the refusal discarded the
// caller's --result, which existed nowhere but argv. On the gh-issue track the
// successor build ticket is not filed until after the human gate, so at the
// moment a triage reports there is no id that can legally satisfy the guard.
// The operator's actual choice was "supply a successor or lose your work", with
// no correct value available, and the only move that got the command through
// was a real id naming the wrong item.
//
// So the tests below come in that order, because the fix does:
//
//  1. THE PRESSURE. A refusal must cost a retry and never the payload, and must
//     SAY so — an operator who watches a command exit non-zero assumes the
//     payload went with it, and acts on that assumption.
//  2. THE VISIBILITY. A wrong-but-real successor must be readable at the
//     callsite. It is printed rather than refused: mg-9259 measured both
//     structural checks against the live store on 2026-08-07 and both over-fire
//     outright — requiring a depends: back-reference would refuse 40 of 40
//     existing links, and refusing an already-terminal successor would refuse
//     29 of 40, most of them designs whose build has legitimately landed. See
//     workitem.SuccessorRef. remainder.go records what a guard firing at that
//     volume costs: mg self-installs on merge, so one that blocks routine
//     completions is removed by whoever it inconveniences.

// claimedSidecar is the path a result takes while its item is still claimed.
// The .md wears a PID suffix there (<id>.md.<pid>); the sidecar does not.
func claimedSidecar(root, id string) string {
	return filepath.Join(root, "work", "claimed", id+".result.json")
}

// --- 1. The pressure: a refusal must not charge for itself -------------------

// TestCLI_DoneRefusalPreservesTheResult is the primary control for the cheap
// half. The guard is still allowed to refuse; it is no longer allowed to bill
// the caller a triage report for the privilege.
func TestCLI_DoneRefusalPreservesTheResult(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id, `--result={"verdict":"IMPLEMENT","repro":"confirmed"}`)
	if code == 0 {
		t.Fatalf("the declared-remainder guard did not fire\n%s", out)
	}

	raw, err := os.ReadFile(claimedSidecar(root, id))
	if err != nil {
		t.Fatalf("the refusal discarded the --result: %v", err)
	}
	if !strings.Contains(string(raw), "IMPLEMENT") {
		t.Errorf("preserved result = %q, want the verdict the caller passed", raw)
	}

	// Preserving the payload must not have completed anything.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=claimed")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s left claimed/ despite the refusal:\n%s", id, listOut)
	}
	doneOut, _ := mgArchive(t, bin, root, "list", "--status=done")
	if strings.Contains(doneOut, id) {
		t.Errorf("%s reached done/ despite the refusal:\n%s", id, doneOut)
	}
}

// TestCLI_DoneRefusalSaysTheResultIsSafe: removing the data loss does not
// remove the pressure on its own. The operator cannot see the filesystem; they
// see a non-zero exit and assume the worst, then reach for whatever id gets the
// command through. The reassurance has to be in the refusal, at the moment and
// place the wrong decision is made.
func TestCLI_DoneRefusalSaysTheResultIsSafe(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage that must not be lost", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id, `--result={"verdict":"IMPLEMENT"}`)
	if code == 0 {
		t.Fatalf("guard did not fire\n%s", out)
	}
	if !strings.Contains(out, "NOT lost") {
		t.Errorf("refusal = %q, want it to state plainly that the --result survived", out)
	}
	if !strings.Contains(out, id+".result.json") {
		t.Errorf("refusal = %q, want it to name where the preserved result is", out)
	}
}

// TestCLI_DoneRefusalWithoutAResultSaysNothingAboutOne: the reassurance is
// conditional on there being something to reassure about. A refusal on a bare
// `mg done` must not claim a result was preserved when none was passed —
// telling an operator their work is safe when nothing was written is the same
// class of lie the ticket is about, pointed the other way.
func TestCLI_DoneRefusalWithoutAResultSaysNothingAboutOne(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "no result passed", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 {
		t.Fatalf("guard did not fire\n%s", out)
	}
	if strings.Contains(out, "NOT lost") {
		t.Errorf("refusal = %q claims a --result was preserved, but none was passed", out)
	}
}

// TestCLI_DoneBadSuccessorAlsoPreservesTheResult: the two refusal paths are one
// situation from the caller's side. `--successor <ghost>` is the case where the
// operator was ALREADY reaching for an id to get through, so losing the payload
// there is the worst possible moment for it.
func TestCLI_DoneBadSuccessorAlsoPreservesTheResult(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage reaching for an id", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id, "--successor=mg-ffff", `--result={"verdict":"IMPLEMENT"}`)
	if code == 0 {
		t.Fatalf("--successor=mg-ffff was accepted\n%s", out)
	}
	raw, err := os.ReadFile(claimedSidecar(root, id))
	if err != nil {
		t.Fatalf("a rejected --successor discarded the --result: %v", err)
	}
	if !strings.Contains(string(raw), "IMPLEMENT") {
		t.Errorf("preserved result = %q, want the verdict the caller passed", raw)
	}
	if !strings.Contains(out, "NOT lost") {
		t.Errorf("refusal = %q, want it to state that the --result survived", out)
	}
}

// TestCLI_DoneRetryAfterRefusalCarriesTheResult is what makes the promise real.
// Preserving the bytes is worthless if the retry — run later, by an agent that
// no longer holds the report in context and passes no --result at all — lands
// the item in done/ without them.
func TestCLI_DoneRetryAfterRefusalCarriesTheResult(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")

	if _, code := mgArchive(t, bin, root, "done", id, `--result={"verdict":"IMPLEMENT"}`); code == 0 {
		t.Fatalf("guard did not fire on the first attempt")
	}

	build := seedAvailable(t, bin, root, "task", "build what the triage recommended")

	// The retry passes NO --result: the payload is the store's problem now.
	out, code := mgArchive(t, bin, root, "done", id, "--successor="+build)
	if code != 0 {
		t.Fatalf("retry after filing the successor: exit %d\n%s", code, out)
	}

	raw, err := os.ReadFile(filepath.Join(root, "work", "done", id+".result.json"))
	if err != nil {
		t.Fatalf("reading the completed item's result: %v", err)
	}
	if !strings.Contains(string(raw), "IMPLEMENT") {
		t.Errorf("completed result = %q, want the verdict from the refused attempt", raw)
	}
	if _, err := os.Stat(claimedSidecar(root, id)); !os.IsNotExist(err) {
		t.Errorf("the preserved sidecar was left behind in claimed/ as a stray")
	}
}

// TestCLI_DoneRetryMergesOntoThePreservedResult: writing the sidecar early made
// the preserved copy a PRIOR result for the retry, which routes it through
// mergeResultSidecar. That is the intended path — Done must never destroy a
// result it did not write — so a retry that adds the branch must not erase the
// verdict the refused attempt saved.
func TestCLI_DoneRetryMergesOntoThePreservedResult(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")

	if _, code := mgArchive(t, bin, root, "done", id, `--result={"verdict":"IMPLEMENT"}`); code == 0 {
		t.Fatalf("guard did not fire on the first attempt")
	}

	build := seedAvailable(t, bin, root, "task", "build what the triage recommended")
	out, code := mgArchive(t, bin, root, "done", id, "--successor="+build, `--result={"branch":"polecat-p9259"}`)
	if code != 0 {
		t.Fatalf("retry with a fresh --result: exit %d\n%s", code, out)
	}

	raw, err := os.ReadFile(filepath.Join(root, "work", "done", id+".result.json"))
	if err != nil {
		t.Fatalf("reading the completed item's result: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "IMPLEMENT") {
		t.Errorf("completed result = %q, want the preserved verdict to survive the retry", got)
	}
	if !strings.Contains(got, "polecat-p9259") {
		t.Errorf("completed result = %q, want the branch the retry passed", got)
	}
}

// --- 2. The visibility: a wrong-but-real successor must be readable ----------

// TestCLI_DoneNamesTheSuccessorItLinked is the primary control for the real
// half. Exit 0 with no output is what made a fabricated id safe to pass; the
// title is what makes it obvious. The STATUS is printed alongside because the
// two failure shapes read differently — a successor in done/ is a tracker for
// nothing, and that is visible in one glance or not at all.
func TestCLI_DoneNamesTheSuccessorItLinked(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	triage := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")
	build := seedAvailable(t, bin, root, "task", "build the thing the triage recommended")

	out, code := mgArchive(t, bin, root, "done", triage, "--successor="+build)
	if code != 0 {
		t.Fatalf("mg done --successor: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, build) {
		t.Errorf("output = %q, want it to name the successor id %s", out, build)
	}
	if !strings.Contains(out, "build the thing the triage recommended") {
		t.Errorf("output = %q, want the successor's TITLE — the id alone is what a wrong one already looks like", out)
	}
	if !strings.Contains(out, "available") {
		t.Errorf("output = %q, want the successor's status", out)
	}
}

// TestCLI_DoneNamesAWrongButRealSuccessor is the ticket's exact scenario. The
// operator, refused and under pressure, supplies a real id belonging to an
// unrelated item. mg still accepts it — nothing cheap can tell the difference,
// and both structural checks were measured over-firing — but it must no longer
// be SILENT about what it just wired up.
func TestCLI_DoneNamesAWrongButRealSuccessor(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	triage := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT", "--declares-remainder")
	unrelated := seedAvailable(t, bin, root, "task", "rotate the deploy credentials")

	out, code := mgArchive(t, bin, root, "done", triage, "--successor="+unrelated)
	if code != 0 {
		t.Fatalf("mg done with a real-but-unrelated successor: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "rotate the deploy credentials") {
		t.Fatalf("output = %q — a wrong-but-real successor completed without naming what it linked; "+
			"that silence is the whole defect", out)
	}
}

// TestCLI_DoneNamesAPreExistingSuccessorTag: the flag is one route to the tag,
// not the thing being reported. An item completing on a successor: tag written
// earlier — by `mg edit --add-tags`, or by a previous run — is exactly as
// capable of naming the wrong item, and gets exactly the same line.
func TestCLI_DoneNamesAPreExistingSuccessorTag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	build := seedAvailable(t, bin, root, "task", "carry the recommendation forward")
	triage := seedClaimedTagged(t, bin, root, "triage tagged ahead of time",
		"--declares-remainder", "--tags=successor:"+build)

	out, code := mgArchive(t, bin, root, "done", triage)
	if code != 0 {
		t.Fatalf("mg done on an item already carrying a successor tag: exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "carry the recommendation forward") {
		t.Errorf("output = %q, want the successor named even though --successor was not passed", out)
	}
}

// TestCLI_DoneWithoutASuccessorPrintsNoSuccessorLine: the overwhelming majority
// of completions have no successor at all. A line printed for them is noise,
// and noise is how the line that matters stops being read.
func TestCLI_DoneWithoutASuccessorPrintsNoSuccessorLine(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "an ordinary task")

	out, code := mgArchive(t, bin, root, "done", id)
	if code != 0 {
		t.Fatalf("mg done on an ordinary task: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "Successor") {
		t.Errorf("output = %q, want no successor line on an item that has none", out)
	}
}
