package main

import (
	"strings"
	"testing"
)

// The end-to-end half of mg-ed7b. The unit tests next door
// (internal/workitem/unclaimhold_test.go) pin the rule; these pin what an
// operator standing at the terminal actually sees, because the whole finding is
// that a careful agent ran the right command with the right care and was told
// nothing.

// TestCLI_UnclaimSaysWhatItIsReleasing. A sweeper releasing a claim left by a
// dead agent gets the one fact its own discriminator cannot produce: this item
// declares that its output is a recommendation, and nothing tracks it. The
// message must name the item — a sweep releases several in a loop — and must
// name the correction.
func TestCLI_UnclaimSaysWhatItIsReleasing(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: the packet is the body", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "unclaim", id)
	if code != 0 {
		t.Fatalf("mg unclaim exited %d — this is a report, not a refusal\n%s", code, out)
	}
	if !strings.Contains(out, "Unclaimed "+id) {
		t.Errorf("output = %q, want the release itself reported", out)
	}
	if !strings.Contains(out, "remainder") {
		t.Errorf("output = %q, want it to say the released item still owes something", out)
	}
	if !strings.Contains(out, "--assignee") {
		t.Errorf("output = %q, want it to name the correction", out)
	}

	// A report must not have changed the outcome.
	listOut, _ := mgArchive(t, bin, root, "list", "--status=available")
	if !strings.Contains(listOut, id) {
		t.Errorf("%s did not reach available/ — unclaim refused instead of reporting:\n%s", id, listOut)
	}
}

// TestCLI_UnclaimIsSilentAboutAnOrdinaryClaim is the population control, and it
// is the half that decides whether the line above survives. Every ordinary task
// released must produce no note at all; a message on the routine case is a
// message nobody reads on the case that matters.
func TestCLI_UnclaimIsSilentAboutAnOrdinaryClaim(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "ordinary work, abandoned mid-flight", "--no-declares-remainder")

	out, code := mgArchive(t, bin, root, "unclaim", id)
	if code != 0 {
		t.Fatalf("mg unclaim exited %d\n%s", code, out)
	}
	if strings.Contains(out, "remainder") {
		t.Errorf("output = %q mentions a remainder on an item that declares none", out)
	}
	if strings.Contains(out, "Note:") {
		t.Errorf("output = %q carries a note on an ordinary release", out)
	}
}

// TestCLI_UnclaimAssigneeGatesTheItemAsItLands. The correction offered above has
// to be a single command, because the two-command form is what produced the
// defect: mg-24d2 was released at 18:24:18Z and assigned at 18:27:15Z, and a
// priority-wake named it as dispatchable in between.
func TestCLI_UnclaimAssigneeGatesTheItemAsItLands(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: awaiting a GO/NO-GO", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "unclaim", id, "--assignee=human")
	if code != 0 {
		t.Fatalf("mg unclaim --assignee exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "Waiting on human") {
		t.Errorf("output = %q, want the recorded gate reported back", out)
	}
	// The note exists to say "nothing on this item says who waits on it". Once
	// something does, repeating it is noise.
	if strings.Contains(out, "Note:") {
		t.Errorf("output = %q still warns although the item landed gated", out)
	}

	showOut, _ := mgArchive(t, bin, root, "show", id)
	if !strings.Contains(showOut, "human") {
		t.Errorf("mg show %s does not carry the assignee:\n%s", id, showOut)
	}
	if !strings.Contains(showOut, "available") {
		t.Errorf("mg show %s is not available after the release:\n%s", id, showOut)
	}
}

// TestCLI_DoneRefusalNamesTheHandoff. This refusal is where the claim-as-hold
// was invented: on the gh-issue track it fires when no successor id can legally
// exist yet, so an agent offered only --successor improvises a hold out of the
// claim. It must name the reachable move — and must still not name the
// retraction, which is the move that would make the guard decorative.
func TestCLI_DoneRefusalNamesTheHandoff(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := seedClaimedTagged(t, bin, root, "triage: verdict IMPLEMENT, gate not yet passed", "--declares-remainder")

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 {
		t.Fatalf("the guard did not fire\n%s", out)
	}
	if !strings.Contains(out, "mg unclaim "+id+" --assignee=human") {
		t.Errorf("refusal = %q, want it to name the hand-off for an item whose successor cannot exist yet", out)
	}
	if strings.Contains(out, "--rm-tags") {
		t.Errorf("refusal = %q teaches the retraction", out)
	}
}
