package main

import (
	"strings"
	"testing"
)

// The declaration is EMITTED BY DEFAULT at creation (mg-966d).
//
// mg-8970 shipped the guard and shipped the declaration as an OPT-IN FLAG.
// Measured sixteen hours later, over the whole live store: ZERO items carried
// the marker — including mg-ee98, the triage that died of exactly this defect,
// and every triage and design filed in the interval by agents holding the ticket
// in context. The guard was running and could not fire. The forgettable step had
// not been removed by putting emission in the tool; it had moved, from "remember
// the tag string" to "remember the flag."
//
// So `mg new` picks the default. The tests below are ordered to prove that the
// default is a DEFAULT and not a blanket: the positive control comes with a
// NEGATIVE control on the same page, because a test that only checks that design
// items carry the tag would pass just as happily if mg applied it to everything,
// and a tag on everything would refuse every completion in the fleet.
//
// The line this must not cross is mg-8970's ruling that the GUARD must not key
// on type. It does not: `mg done` still reads the tag. See
// TestCLI_DoneDesignWithoutDeclarationStillCompletes next door, which files a
// `type: design` item with the escape and completes it, proving the guard reads
// the declaration rather than the type.

// filedItem runs `mg new` with the given args and returns the created id.
func filedItem(t *testing.T, bin, root string, args ...string) string {
	t.Helper()
	out, code := mgArchive(t, bin, root, append([]string{"new"}, args...)...)
	if code != 0 {
		t.Fatalf("mg new %v: exit %d\n%s", args, code, out)
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

// declares reports whether the created item carries the declaration tag.
func declares(t *testing.T, bin, root, id string) bool {
	t.Helper()
	out, code := mgArchive(t, bin, root, "show", id)
	if code != 0 {
		t.Fatalf("mg show %s: exit %d\n%s", id, code, out)
	}
	return strings.Contains(out, "declares-remainder")
}

// --- The default, and the control that makes it mean something -------------

// TestCLI_NewDesignDeclaresByDefault is the acceptance criterion: no flag
// passed, and the item carries the marker.
func TestCLI_NewDesignDeclaresByDefault(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=design", "--title=a design filed by someone who never read mg-8970")
	if !declares(t, bin, root, id) {
		t.Fatalf("%s (type: design, no flags) does not declare a remainder — "+
			"the declaration is opt-in again and the guard is inert", id)
	}
}

// TestCLI_NewTaskDoesNotDeclareByDefault is the POSITIVE CONTROL, and it is the
// test that makes the one above worth writing. `task` is 1,876 of the store's
// 1,955 items. If the tag were applied unconditionally the design test would
// still pass, and every ordinary completion in the fleet would then be refused
// until someone filed a successor for it — which is how a guard gets deleted by
// whoever it inconveniences (mg-3412), taking the real cases with it.
func TestCLI_NewTaskDoesNotDeclareByDefault(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=task", "--title=an ordinary task that owes nothing")
	if declares(t, bin, root, id) {
		t.Fatalf("%s (type: task) declares a remainder — the default is not a default, "+
			"it is a blanket, and every routine completion is now refused", id)
	}

	// The default must also stay off for the other types that DO the thing
	// rather than recommend it. Each is a separate way for the map to grow a
	// wrong entry.
	for _, typ := range []string{"bug", "chore", "doc", "qa"} {
		other := filedItem(t, bin, root, "--type="+typ, "--title=an ordinary "+typ)
		if declares(t, bin, root, other) {
			t.Errorf("%s (type: %s) declares a remainder; only recommendation-shaped types default on", other, typ)
		}
	}
}

// TestCLI_NewRecommendationTypesDeclareByDefault: design is the one the ticket
// names, but it is not the only type whose completion leaves its output undone.
func TestCLI_NewRecommendationTypesDeclareByDefault(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, typ := range []string{"design", "scoping", "audit", "idea"} {
		id := filedItem(t, bin, root, "--type="+typ, "--title=a "+typ+" whose output somebody else has to carry")
		if !declares(t, bin, root, id) {
			t.Errorf("%s (type: %s) does not declare a remainder", id, typ)
		}
	}
}

// TestCLI_NewTriageBodyDeclaresByDefault is the mg-ee98 shape and the reason the
// type alone is not enough. That item was `type: task`, its verdict was
// IMPLEMENT on a reproduced data-loss mechanism, and nothing carried the fix:
// triage is a position in the workflow, not a type, so the default reads the
// body's leading carrier block as well.
func TestCLI_NewTriageBodyDeclaresByDefault(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	body := bodyFileArg(t, "workflow: gh-issue\nstage: triage\ngh: drellem2/pogo#94\n\nVerdict: IMPLEMENT.\n")
	id := filedItem(t, bin, root, "--type=task", "--title=triage: gh#94", body)
	if !declares(t, bin, root, id) {
		t.Fatalf("%s is a type:task triage and declares nothing — this is exactly mg-ee98", id)
	}
}

// TestCLI_NewNonTriageStageDoesNotDeclare keeps the body half of the default as
// narrow as the type half. A carrier block at any other stage is a workflow
// position whose remainder is in flight, not owed as a new ticket — the
// over-fire mg-8970 measured at 41 items when a predicate treated every
// non-terminal stage as owing something.
func TestCLI_NewNonTriageStageDoesNotDeclare(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	for _, stage := range []string{"gated", "build", "review", "merge"} {
		body := bodyFileArg(t, "workflow: gh-issue\nstage: "+stage+"\ngh: drellem2/pogo#94\n\nIn flight.\n")
		id := filedItem(t, bin, root, "--type=task", "--title=gh-issue at "+stage, body)
		if declares(t, bin, root, id) {
			t.Errorf("%s (stage: %s) declares a remainder; only stage: triage defaults on", id, stage)
		}
	}
}

// TestCLI_NewTriageWordInProseDoesNotDeclare: the default reads the LEADING
// CARRIER BLOCK, not the prose. A body that discusses triage — as most triage
// tickets and this one's own do — must not be able to reach back and mark the
// item, or the marker means whatever the nearest paragraph happens to say.
func TestCLI_NewTriageWordInProseDoesNotDeclare(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	body := bodyFileArg(t, "Fix the bug.\n\nThe triage said this was urgent.\nstage: triage\n")
	id := filedItem(t, bin, root, "--type=task", "--title=build the fix the triage asked for", body)
	if declares(t, bin, root, id) {
		t.Fatalf("%s declares a remainder because its PROSE mentions a stage; "+
			"the default must read the leading carrier block only", id)
	}
}

// --- The escape, which must exist and must be tested -----------------------

// TestCLI_NewNoDeclaresRemainderSuppresses: a default nobody can turn off is not
// a default, it is a rule — and this one is wrong often enough to need an out (a
// design abandoned rather than implemented, a triage concluding nothing is
// owed).
func TestCLI_NewNoDeclaresRemainderSuppresses(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=design", "--no-declares-remainder", "--title=a design we will not be building")
	if declares(t, bin, root, id) {
		t.Fatalf("--no-declares-remainder did not suppress the declaration on %s", id)
	}

	// The escape has to be worth something: the item must then complete.
	if out, code := mgArchive(t, bin, root, "claim", id); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", id, code, out)
	}
	if out, code := mgArchive(t, bin, root, "done", id); code != 0 {
		t.Fatalf("mg done on an opted-out design: exit %d — the escape does not escape\n%s", code, out)
	}
}

// TestCLI_NewNoDeclaresRemainderSuppressesATriageBody: the body half needs its
// own escape test, because it is reached by a different branch than the type.
func TestCLI_NewNoDeclaresRemainderSuppressesATriageBody(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	body := bodyFileArg(t, "workflow: gh-issue\nstage: triage\ngh: drellem2/pogo#94\n\nVerdict: WONTFIX.\n")
	id := filedItem(t, bin, root, "--type=task", "--no-declares-remainder", "--title=triage: gh#94 wontfix", body)
	if declares(t, bin, root, id) {
		t.Fatalf("--no-declares-remainder did not suppress the triage-body default on %s", id)
	}
}

// TestCLI_NewDeclaresRemainderStillForcesItOn: the opt-in flag is not retired.
// A task can conclude in a recommendation, and mg cannot know that from the
// type, so the filer must still be able to say so.
func TestCLI_NewDeclaresRemainderStillForcesItOn(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=task", "--declares-remainder", "--title=an investigation that ends in a proposal")
	if !declares(t, bin, root, id) {
		t.Fatalf("--declares-remainder on a task did not write the declaration on %s", id)
	}
}

// TestCLI_NewBothDeclarationFlagsIsRefused: the two flags are an explicit
// answer, and passing both is not an answer. Picking one silently would decide
// the item's completion rule by flag order.
func TestCLI_NewBothDeclarationFlagsIsRefused(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	out, code := mgArchive(t, bin, root, "new", "--title=contradicting myself",
		"--declares-remainder", "--no-declares-remainder")
	if code == 0 {
		t.Fatalf("mg new took both declaration flags\n%s", out)
	}
	if !strings.Contains(out, "--no-declares-remainder") {
		t.Errorf("refusal = %q, want it to name both flags", out)
	}
}

// TestCLI_NewNoDeclaresRemainderOnATaskIsANoOp: the escape must be harmless
// where there is nothing to escape, so a filer or template can pass it
// unconditionally without having to know the type's default.
func TestCLI_NewNoDeclaresRemainderOnATaskIsANoOp(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=task", "--no-declares-remainder", "--title=an ordinary task")
	if declares(t, bin, root, id) {
		t.Errorf("%s declares a remainder despite --no-declares-remainder", id)
	}
}

// --- The default reaches the guard -----------------------------------------

// TestCLI_DefaultedDeclarationActuallyStopsDone is the end-to-end that the
// adoption count is a proxy for. A design filed with no flags at all must be
// refused at `mg done`, and must pass once a successor names what carries it
// forward. Everything above tests a tag; this tests that the tag is load-bearing
// on the path that let mg-ee98 through.
func TestCLI_DefaultedDeclarationActuallyStopsDone(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=design", "--title=a design recommending a build")
	if out, code := mgArchive(t, bin, root, "claim", id); code != 0 {
		t.Fatalf("mg claim %s: exit %d\n%s", id, code, out)
	}

	out, code := mgArchive(t, bin, root, "done", id)
	if code == 0 {
		t.Fatalf("mg done took a defaulted design with no successor — the marker is on the item "+
			"but the guard does not read it\n%s", out)
	}
	if !strings.Contains(out, "--successor") {
		t.Errorf("refusal = %q, want the remedy '--successor <id>'", out)
	}

	build := seedAvailable(t, bin, root, "task", "build what the design recommends")
	if out, code := mgArchive(t, bin, root, "done", id, "--successor="+build); code != 0 {
		t.Fatalf("mg done %s --successor=%s: exit %d — the guard refuses even when discharged\n%s",
			id, build, code, out)
	}
}

// TestCLI_DefaultedDeclarationWritesOneCanonicalTag: the default and an
// explicitly passed tag must not file two copies, the same property mg-8970
// pinned for the flag.
func TestCLI_DefaultedDeclarationWritesOneCanonicalTag(t *testing.T) {
	bin := buildBinary(t)
	root := archiveTestRoot(t, bin)

	id := filedItem(t, bin, root, "--type=design", "--title=declared twice over",
		"--tags=declares-remainder,urgent")

	out, _ := mgArchive(t, bin, root, "show", id)
	if n := strings.Count(out, "declares-remainder"); n != 1 {
		t.Errorf("item carries %d copies of the declaration tag, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "urgent") {
		t.Errorf("the default dropped the caller's own tags:\n%s", out)
	}
}
